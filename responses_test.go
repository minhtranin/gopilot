package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUsesResponsesAPI(t *testing.T) {
	if !usesResponsesAPI("gpt-5.6-luna") {
		t.Fatal("gpt-5.6-luna must use Responses API")
	}
	if usesResponsesAPI("gemini-3.8-flash") {
		t.Fatal("chat-completions model must keep existing route")
	}
}

func TestToResponsesInputConvertsToolsAndImages(t *testing.T) {
	messages := []OpenAIMessage{
		{Role: "user", Content: []OpenAIContentPart{{Type: "text", Text: "look"}, {Type: "image_url", ImageURL: &OpenAIImgURL{URL: "data:image/png;base64,abc"}}}},
		{Role: "assistant", ToolCalls: []OpenAIToolCall{{ID: "call-1", Type: "function", Function: OpenAIToolCallFunc{Name: "Bash", Arguments: `{"command":"pwd"}`}}}},
		{Role: "tool", ToolCallID: "call-1", Content: "/tmp"},
	}
	body, err := json.Marshal(toResponsesInput(messages))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{"input_image", "function_call", "function_call_output", "call-1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestTranslateResponsesToAnthropicToolUse(t *testing.T) {
	var response responsesResponse
	if err := json.Unmarshal([]byte(`{
		"id":"resp-1","model":"gpt-5.6-luna","status":"completed",
		"output":[
			{"type":"message","content":[{"type":"output_text","text":"checking"}]},
			{"type":"function_call","call_id":"call-1","name":"Bash","arguments":"{\"command\":\"pwd\"}"}
		],
		"usage":{"input_tokens":12,"output_tokens":4,"input_tokens_details":{"cached_tokens":2}}
	}`), &response); err != nil {
		t.Fatal(err)
	}
	got := translateResponsesToAnthropic(response)
	if got.StopReason == nil || *got.StopReason != "tool_use" {
		t.Fatalf("unexpected stop reason: %v", got.StopReason)
	}
	if len(got.Content) != 2 || got.Content[1].Name != "Bash" || got.Content[1].ID != "call-1" {
		t.Fatalf("unexpected content: %#v", got.Content)
	}
	if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 4 {
		t.Fatalf("unexpected usage: %#v", got.Usage)
	}
}
