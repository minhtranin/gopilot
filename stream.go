package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

var debugStream = os.Getenv("GOPILOT_DEBUG_STREAM") != ""

func logStream(s string) { log.Println("[gopilot]", s) }

// sseState mirrors the JS port's per-connection state: Anthropic content
// blocks are indexed and must be opened/closed explicitly, but OpenAI just
// streams deltas — this is what reconstructs block boundaries from that.
type sseState struct {
	messageStartSent bool
	blockIndex       int
	blockOpen        bool
	toolBlockIndex   map[int]int // OpenAI tool_calls[].index -> our anthropic block index
	toolBlockIsTool  map[int]bool
}

func newSSEState() *sseState {
	return &sseState{toolBlockIndex: map[int]int{}, toolBlockIsTool: map[int]bool{}}
}

type sseEvent struct {
	Type string
	Data any
}

// translate returns the Anthropic events for this chunk, plus emptyError —
// true when Copilot's backend died (finish_reason:"error") before this
// turn ever produced real content. Confirmed live (2026-08-24): Gemini via
// Copilot can die mid-stream right after a reasoning_text delta, no error
// detail, always before any visible content_block was opened. The caller
// decides what to do with emptyError — retry the whole request (safe: the
// client has seen nothing from this turn yet) or, out of retries, synthesize
// a visible fallback instead of forwarding a turn the SDK can't parse.
func (st *sseState) translate(chunk OpenAIStreamChunk) (events []sseEvent, emptyError bool) {
	if len(chunk.Choices) == 0 {
		return nil, false
	}
	choice := chunk.Choices[0]

	if !st.messageStartSent {
		usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
		if chunk.Usage != nil {
			cached := 0
			if chunk.Usage.PromptDetails != nil {
				cached = chunk.Usage.PromptDetails.CachedTokens
				usage["cache_read_input_tokens"] = cached
			}
			usage["input_tokens"] = chunk.Usage.PromptTokens - cached
		}
		events = append(events, sseEvent{"message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": chunk.ID, "type": "message", "role": "assistant",
				"content": []any{}, "model": chunk.Model,
				"stop_reason": nil, "stop_sequence": nil, "usage": usage,
			},
		}})
		st.messageStartSent = true
	}

	if choice.Delta.Content != "" {
		if st.blockOpen && st.blockIsToolAtCurrentIndex() {
			events = append(events, st.closeBlock())
		}
		if !st.blockOpen {
			events = append(events, sseEvent{"content_block_start", map[string]any{
				"type": "content_block_start", "index": st.blockIndex,
				"content_block": map[string]any{"type": "text", "text": ""},
			}})
			st.blockOpen = true
		}
		events = append(events, sseEvent{"content_block_delta", map[string]any{
			"type": "content_block_delta", "index": st.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": choice.Delta.Content},
		}})
	}

	for _, tc := range choice.Delta.ToolCalls {
		if tc.ID != "" && tc.Function.Name != "" {
			if st.blockOpen {
				events = append(events, st.closeBlock())
			}
			idx := st.blockIndex
			st.toolBlockIndex[tc.Index] = idx
			st.toolBlockIsTool[idx] = true
			events = append(events, sseEvent{"content_block_start", map[string]any{
				"type": "content_block_start", "index": idx,
				"content_block": map[string]any{
					"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": map[string]any{},
				},
			}})
			st.blockOpen = true
		}
		if tc.Function.Arguments != "" {
			if idx, ok := st.toolBlockIndex[tc.Index]; ok {
				events = append(events, sseEvent{"content_block_delta", map[string]any{
					"type": "content_block_delta", "index": idx,
					"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
				}})
			}
		}
	}

	if choice.FinishReason != nil {
		if *choice.FinishReason == "error" && st.blockIndex == 0 && !st.blockOpen {
			return events, true
		}
		if st.blockOpen {
			events = append(events, st.closeBlock())
		}
		usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
		if chunk.Usage != nil {
			cached := 0
			if chunk.Usage.PromptDetails != nil {
				cached = chunk.Usage.PromptDetails.CachedTokens
				usage["cache_read_input_tokens"] = cached
			}
			usage["input_tokens"] = chunk.Usage.PromptTokens - cached
			usage["output_tokens"] = chunk.Usage.CompletionTokens
		}
		events = append(events, sseEvent{"message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   mapStopReason(choice.FinishReason),
				"stop_sequence": nil,
			},
			"usage": usage,
		}})
		events = append(events, sseEvent{"message_stop", map[string]any{"type": "message_stop"}})
	}
	return events, false
}

func (st *sseState) blockIsToolAtCurrentIndex() bool {
	return st.toolBlockIsTool[st.blockIndex]
}

func (st *sseState) closeBlock() sseEvent {
	ev := sseEvent{"content_block_stop", map[string]any{
		"type": "content_block_stop", "index": st.blockIndex,
	}}
	st.blockIndex++
	st.blockOpen = false
	return ev
}

func writeSSE(w io.Writer, flusher http.Flusher, ev sseEvent) {
	b, _ := json.Marshal(ev.Data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
	if flusher != nil {
		flusher.Flush()
	}
}

const maxStreamAttempts = 3

// backoffFor honors Copilot's actual Retry-After on a 429 instead of
// guessing — retrying instantly just re-joins the same burst and gets
// 429'd again (observed live: retries got rejected in the same second).
func backoffFor(err error) time.Duration {
	var rl rateLimitError
	if errors.As(err, &rl) {
		return rl.retryAfter
	}
	return 400 * time.Millisecond
}

// streamWithRetry drives the whole /v1/messages streaming response,
// including retrying the entire Copilot call when it dies before producing
// any real content. Events are buffered until the first real
// content_block_start; once that lands we've "committed" to this attempt
// and stream live from then on — a turn that fails AFTER real content was
// already sent can't be retried without double-sending, so that case (rare
// in practice — every observed failure died before any content) falls
// through to a plain forwarded error turn instead.
func streamWithRetry(fetch func() (*http.Response, error), w http.ResponseWriter, flusher http.Flusher) {
	for attempt := 1; attempt <= maxStreamAttempts; attempt++ {
		resp, err := fetch()
		if err != nil {
			wait := backoffFor(err)
			logStream(fmt.Sprintf("attempt %d/%d: request failed: %v (waiting %s)", attempt, maxStreamAttempts, err, wait))
			time.Sleep(wait)
			continue
		}

		sst := newSSEState()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

		var pending []sseEvent
		committed := false
		gotEmptyError := false

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" || data == "" {
				continue
			}
			if debugStream {
				logStream("raw copilot chunk: " + data)
			}
			var chunk OpenAIStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			events, emptyErr := sst.translate(chunk)
			if emptyErr {
				gotEmptyError = true
				break
			}
			for _, ev := range events {
				if committed {
					writeSSE(w, flusher, ev)
					continue
				}
				pending = append(pending, ev)
				if ev.Type == "content_block_start" {
					committed = true
					for _, p := range pending {
						writeSSE(w, flusher, p)
					}
					pending = nil
				}
			}
		}
		scanErr := scanner.Err()
		resp.Body.Close()

		if committed {
			if scanErr != nil {
				logStream("stream error after content already sent: " + scanErr.Error())
			}
			return // real content reached the client — done, win or lose
		}
		if scanErr != nil {
			logStream(fmt.Sprintf("attempt %d/%d: read error before any content: %v", attempt, maxStreamAttempts, scanErr))
			time.Sleep(400 * time.Millisecond)
			continue
		}
		if gotEmptyError {
			logStream(fmt.Sprintf("attempt %d/%d: Copilot backend died before any content, retrying", attempt, maxStreamAttempts))
			time.Sleep(400 * time.Millisecond)
			continue
		}
		return // stream ended cleanly with genuinely nothing to say — not an error, don't retry
	}

	logStream(fmt.Sprintf("all %d attempts failed before any content — surfacing visible error", maxStreamAttempts))
	emitFallbackTurn(w, flusher)
}

func emitFallbackTurn(w http.ResponseWriter, flusher http.Flusher) {
	msg := "[gopilot: Copilot backend lỗi giữa chừng nhiều lần liên tiếp — thử lại câu hỏi sau]"
	events := []sseEvent{
		{"message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "gopilot-fallback", "type": "message", "role": "assistant",
				"content": []any{}, "model": "", "stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
			},
		}},
		{"content_block_start", map[string]any{
			"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""},
		}},
		{"content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": msg},
		}},
		{"content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}},
		{"message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		}},
		{"message_stop", map[string]any{"type": "message_stop"}},
	}
	for _, ev := range events {
		writeSSE(w, flusher, ev)
	}
}
