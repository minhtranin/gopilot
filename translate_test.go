package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolResultImageReachesResponsesInput(t *testing.T) {
	message := AnthropicMsg{
		Role:    "user",
		Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"read-1","content":[{"type":"text","text":"2536×924"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc123"}}]}]`),
	}

	messages := handleUserMessage(message)
	if len(messages) != 2 {
		t.Fatalf("expected tool output plus image message, got %#v", messages)
	}
	if messages[0].Role != "tool" || messages[0].Content != "2536×924" {
		t.Fatalf("tool output text was not preserved: %#v", messages[0])
	}

	input, err := json.Marshal(toResponsesInput(messages))
	if err != nil {
		t.Fatal(err)
	}
	got := string(input)
	for _, want := range []string{"function_call_output", "input_image", "data:image/png;base64,abc123"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestHandleUserMessage_PreservesEmptyBlockArray(t *testing.T) {
	message := AnthropicMsg{Role: "user", Content: json.RawMessage(`[]`)}
	got := handleUserMessage(message)
	if len(got) != 1 || got[0].Role != "user" || got[0].Content != "" {
		t.Fatalf("empty user turn was dropped: %#v", got)
	}
}

func TestNormalizeAssistantTail_AppendsContinuationForAffectedModels(t *testing.T) {
	messages := []OpenAIMessage{{Role: "user", Content: "first"}, {Role: "assistant", Content: "prefill"}}
	for _, model := range []string{"gemini-3.8-flash", "claude-sonnet-5.5", "claude-opus-5.5"} {
		got := normalizeAssistantTail(model, messages)
		if len(got) != 3 || got[2].Role != "user" || got[2].Content != "Continue." {
			t.Fatalf("unexpected normalized messages for %s: %#v", model, got)
		}
	}
	if len(messages) != 2 {
		t.Fatalf("normalizer mutated input slice: %#v", messages)
	}
}

func TestNormalizeAssistantTail_LeavesValidAndSupportedConversations(t *testing.T) {
	assistantTail := []OpenAIMessage{{Role: "assistant", Content: "tail"}}
	if got := normalizeAssistantTail("gemini-3.8-flash", []OpenAIMessage{{Role: "user", Content: "ok"}}); len(got) != 1 {
		t.Fatalf("user-ended Gemini conversation changed: %#v", got)
	}
	if got := normalizeAssistantTail("claude-sonnet-4", assistantTail); len(got) != 1 {
		t.Fatalf("supported assistant prefill changed: %#v", got)
	}
}

func TestNormalizeToolTurns_GroupsParallelCallsAndDefersImages(t *testing.T) {
	callA := OpenAIToolCall{ID: "read-a", Type: "function", Function: OpenAIToolCallFunc{Name: "Read"}}
	callB := OpenAIToolCall{ID: "read-b", Type: "function", Function: OpenAIToolCallFunc{Name: "Read"}}
	image := OpenAIMessage{Role: "user", Content: []OpenAIContentPart{{Type: "image_url"}}}
	messages := []OpenAIMessage{
		{Role: "assistant", ToolCalls: []OpenAIToolCall{callA}},
		{Role: "assistant", ToolCalls: []OpenAIToolCall{callB}},
		{Role: "tool", ToolCallID: "read-b", Content: "result b"},
		image,
		{Role: "tool", ToolCallID: "read-a", Content: "result a"},
	}

	got := normalizeToolTurns(messages)
	if len(got) != 4 {
		t.Fatalf("expected assistant, two results, then image; got %#v", got)
	}
	if got[0].Role != "assistant" || len(got[0].ToolCalls) != 2 {
		t.Fatalf("parallel calls were not grouped: %#v", got[0])
	}
	if got[1].Role != "tool" || got[1].ToolCallID != "read-a" || got[2].Role != "tool" || got[2].ToolCallID != "read-b" {
		t.Fatalf("results were not placed immediately after calls in call order: %#v", got)
	}
	if got[3].Role != "user" {
		t.Fatalf("image was not deferred until after all results: %#v", got)
	}
}

func TestNormalizeToolTurns_DropsOnlyCallsMissingResults(t *testing.T) {
	messages := []OpenAIMessage{
		{Role: "assistant", Content: "checking", ToolCalls: []OpenAIToolCall{
			{ID: "present", Type: "function", Function: OpenAIToolCallFunc{Name: "Read"}},
			{ID: "missing", Type: "function", Function: OpenAIToolCallFunc{Name: "Read"}},
		}},
		{Role: "tool", ToolCallID: "present", Content: "ok"},
	}

	got := normalizeToolTurns(messages)
	if len(got) != 2 || len(got[0].ToolCalls) != 1 || got[0].ToolCalls[0].ID != "present" {
		t.Fatalf("missing-result call was not removed cleanly: %#v", got)
	}
	if got[0].Content != "checking" || got[1].ToolCallID != "present" {
		t.Fatalf("valid assistant content or result changed: %#v", got)
	}
}
