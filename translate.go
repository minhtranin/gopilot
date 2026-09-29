package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

// injectedMarkerRE matches system-prompt blocks Claude Code's own SDK stuffs
// in front of real instructions — e.g. "x-anthropic-billing-header: cc_version=
// ...". Confirmed live (2026-08-24): Copilot-hosted claude-haiku-4.5 reads
// header-shaped text sitting in the system prompt as a suspicious injection
// and discounts the WHOLE system block, including the user's actual
// instructions right after it. 4 isolated /v1/messages calls on this
// deployment: strip this line -> system.md obeyed every time; leave it in
// -> ignored regardless of tool defs or block order. DeepSeek's backend
// (ccd.fish) gets the identical injected line and isn't thrown by it, which
// is why this only shows up on the Copilot path.
var injectedMarkerRE = regexp.MustCompile(`(?i)^x-anthropic-billing-header:`)

func translateModelName(model string) string {
	// Copilot only lists bare "claude-sonnet-4"/"claude-opus-4" — a
	// date-suffixed id like "claude-sonnet-4-20250514" 404s.
	switch {
	case strings.HasPrefix(model, "claude-sonnet-4-"):
		return "claude-sonnet-4"
	case strings.HasPrefix(model, "claude-opus-4-"):
		return "claude-opus-4"
	default:
		return model
	}
}

func translateToOpenAI(a AnthropicRequest) OpenAIRequest {
	model := translateModelName(a.Model)
	return OpenAIRequest{
		Model:       model,
		Messages:    normalizeAssistantTail(model, translateMessages(a.Messages, a.System)),
		MaxTokens:   a.MaxTokens,
		Stop:        a.StopSequences,
		Stream:      a.Stream,
		Temperature: a.Temperature,
		TopP:        a.TopP,
		User:        userID(a),
		Tools:       translateTools(a.Tools),
		ToolChoice:  translateToolChoice(a.ToolChoice),
		// Gemini via Copilot is a reasoning model: with no reasoning_effort
		// set, it burns the max_tokens budget on hidden thinking and cuts
		// the visible answer off after a handful of tokens (finish_reason
		// "length" at ~14 tokens on a 500-token budget, reproduced live).
		// "low" leaves the budget for actual output instead. NOT "minimal" —
		// gemini-3.7-flash 400s on it ("supported values: [low medium high]"),
		// only 3.5/3.6-flash accept minimal; "low" is the one value every
		// gemini model on Copilot has accepted so far.
		ReasoningEffort: reasoningEffortFor(model),
	}
}

func reasoningEffortFor(model string) string {
	if strings.HasPrefix(model, "gemini") {
		return "low"
	}
	return ""
}

// Gemini and Claude Sonnet 5.5 reject a Chat Completions request whose final
// message is assistant, while the Anthropic client can send an assistant
// prefill/history tail. Add a neutral continuation turn for affected models;
// tool-result and user-ended conversations remain byte-for-byte equivalent.
func normalizeAssistantTail(model string, messages []OpenAIMessage) []OpenAIMessage {
	rejectsAssistantTail := strings.HasPrefix(model, "gemini") || model == "claude-sonnet-5.5"
	if !rejectsAssistantTail || len(messages) == 0 {
		return messages
	}
	if messages[len(messages)-1].Role != "assistant" {
		return messages
	}
	out := append([]OpenAIMessage(nil), messages...)
	out = append(out, OpenAIMessage{Role: "user", Content: "Continue."})
	return out
}

func userID(a AnthropicRequest) string {
	if a.Metadata != nil {
		return a.Metadata.UserID
	}
	return ""
}

func translateMessages(msgs []AnthropicMsg, system json.RawMessage) []OpenAIMessage {
	out := handleSystemPrompt(system)
	for _, m := range msgs {
		if m.Role == "user" {
			out = append(out, handleUserMessage(m)...)
		} else {
			out = append(out, handleAssistantMessage(m)...)
		}
	}
	return out
}

func handleSystemPrompt(system json.RawMessage) []OpenAIMessage {
	blocks := decodeBlocks(system)
	if len(blocks) == 0 {
		return nil
	}
	var kept []string
	for _, b := range blocks {
		if injectedMarkerRE.MatchString(strings.TrimSpace(b.Text)) {
			continue // the fix: drop it before the model ever sees it
		}
		kept = append(kept, b.Text)
	}
	if len(kept) == 0 {
		return nil
	}
	return []OpenAIMessage{{Role: "system", Content: strings.Join(kept, "\n\n")}}
}

func handleUserMessage(m AnthropicMsg) []OpenAIMessage {
	blocks := decodeBlocks(m.Content)
	if blocks == nil {
		// content wasn't a JSON string either -> pass through raw as text
		var s string
		json.Unmarshal(m.Content, &s)
		return []OpenAIMessage{{Role: "user", Content: s}}
	}
	if len(blocks) == 0 {
		// Preserve an explicit empty Anthropic user turn. Dropping it can leave
		// an assistant message last, which Gemini rejects before generation.
		return []OpenAIMessage{{Role: "user", Content: ""}}
	}
	var toolResults, other []ContentBlock
	for _, b := range blocks {
		if b.Type == "tool_result" {
			toolResults = append(toolResults, b)
		} else {
			other = append(other, b)
		}
	}
	var out []OpenAIMessage
	for _, tr := range toolResults {
		out = append(out, OpenAIMessage{
			Role:       "tool",
			ToolCallID: tr.ToolUseID,
			Content:    mapToolResultContent(tr.Content),
		})
		// Anthropic tool results may contain an image returned by a vision-capable
		// tool (for example Claude Code's Read tool). OpenAI tool/function output
		// is text-only in the Responses adapter, so keep the function output text
		// and send the image as a following user multimodal message instead of
		// silently dropping the pixels.
		if images := toolResultImages(tr.Content); len(images) > 0 {
			out = append(out, OpenAIMessage{Role: "user", Content: mapContent(images)})
		}
	}
	if len(other) > 0 {
		out = append(out, OpenAIMessage{Role: "user", Content: mapContent(other)})
	}
	return out
}

func mapToolResultContent(raw json.RawMessage) string {
	blocks := decodeBlocks(raw)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" || b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func toolResultImages(raw json.RawMessage) []ContentBlock {
	blocks := decodeBlocks(raw)
	var images []ContentBlock
	for _, b := range blocks {
		if b.Type == "image" && b.Source != nil {
			images = append(images, b)
		}
	}
	return images
}

func handleAssistantMessage(m AnthropicMsg) []OpenAIMessage {
	blocks := decodeBlocks(m.Content)
	var toolUse, text, thinking []ContentBlock
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			toolUse = append(toolUse, b)
		case "text":
			text = append(text, b)
		case "thinking":
			thinking = append(thinking, b)
		}
	}
	if len(toolUse) == 0 {
		return []OpenAIMessage{{Role: "assistant", Content: mapContent(blocks)}}
	}
	var allText []string
	for _, b := range text {
		allText = append(allText, b.Text)
	}
	for _, b := range thinking {
		allText = append(allText, b.Thinking)
	}
	var calls []OpenAIToolCall
	for _, tu := range toolUse {
		calls = append(calls, OpenAIToolCall{
			ID:   tu.ID,
			Type: "function",
			Function: OpenAIToolCallFunc{
				Name:      tu.Name,
				Arguments: string(tu.Input),
			},
		})
	}
	var content interface{}
	if joined := strings.Join(allText, "\n\n"); joined != "" {
		content = joined
	}
	return []OpenAIMessage{{Role: "assistant", Content: content, ToolCalls: calls}}
}

// mapContent: plain string when there's no image (matches JS behavior —
// avoids the multi-part array shape unless a request actually needs it).
func mapContent(blocks []ContentBlock) interface{} {
	hasImage := false
	for _, b := range blocks {
		if b.Type == "image" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		var parts []string
		for _, b := range blocks {
			switch b.Type {
			case "text":
				parts = append(parts, b.Text)
			case "thinking":
				parts = append(parts, b.Thinking)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	var out []OpenAIContentPart
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, OpenAIContentPart{Type: "text", Text: b.Text})
		case "thinking":
			out = append(out, OpenAIContentPart{Type: "text", Text: b.Thinking})
		case "image":
			if b.Source != nil {
				out = append(out, OpenAIContentPart{
					Type: "image_url",
					ImageURL: &OpenAIImgURL{
						URL: "data:" + b.Source.MediaType + ";base64," + b.Source.Data,
					},
				})
			}
		}
	}
	return out
}

func translateTools(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil
	}
	type fn struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	out := make([]fn, len(tools))
	for i, t := range tools {
		out[i].Type = "function"
		out[i].Function.Name = t.Name
		out[i].Function.Description = t.Description
		out[i].Function.Parameters = t.InputSchema
	}
	b, _ := json.Marshal(out)
	return b
}

func translateToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil
	}
	switch tc.Type {
	case "auto":
		return json.RawMessage(`"auto"`)
	case "any":
		return json.RawMessage(`"required"`)
	case "none":
		return json.RawMessage(`"none"`)
	case "tool":
		if tc.Name == "" {
			return nil
		}
		b, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]string{"name": tc.Name},
		})
		return b
	default:
		return nil
	}
}
