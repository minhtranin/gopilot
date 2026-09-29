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
	for _, model := range []string{"gemini-3.8-flash", "claude-sonnet-5.5"} {
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
