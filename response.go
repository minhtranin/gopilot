package main

import "encoding/json"

var stopReasonMap = map[string]string{
	"stop":           "end_turn",
	"length":         "max_tokens",
	"tool_calls":     "tool_use",
	"content_filter": "end_turn",
	// backend fault mid-stream — always paired with the synthesized error
	// text block in stream.go, never a real tool call, so never "tool_use"
	"error": "end_turn",
}

func mapStopReason(finish *string) *string {
	if finish == nil {
		return nil
	}
	if v, ok := stopReasonMap[*finish]; ok {
		return &v
	}
	return nil
}

func translateToAnthropic(r OpenAIResponse) AnthropicResponse {
	var content []ContentBlock
	var stopReason *string
	if len(r.Choices) > 0 {
		stopReason = r.Choices[0].FinishReason
	}
	for _, choice := range r.Choices {
		content = append(content, textBlocks(choice.Message.Content)...)
		content = append(content, toolUseBlocks(choice.Message.ToolCalls)...)
	}
	usage := AnthropicUsage{}
	if r.Usage != nil {
		cached := 0
		if r.Usage.PromptDetails != nil {
			cached = r.Usage.PromptDetails.CachedTokens
			usage.CacheReadInputTokens = &cached
		}
		usage.InputTokens = r.Usage.PromptTokens - cached
		usage.OutputTokens = r.Usage.CompletionTokens
	}
	return AnthropicResponse{
		ID:         r.ID,
		Type:       "message",
		Role:       "assistant",
		Model:      r.Model,
		Content:    content,
		StopReason: mapStopReason(stopReason),
		Usage:      usage,
	}
}

func textBlocks(raw json.RawMessage) []ContentBlock {
	blocks := decodeBlocks(raw)
	var out []ContentBlock
	for _, b := range blocks {
		if b.Type == "text" || (b.Type == "" && b.Text != "") {
			out = append(out, ContentBlock{Type: "text", Text: b.Text})
		}
	}
	return out
}

func toolUseBlocks(calls []OpenAIToolCall) []ContentBlock {
	var out []ContentBlock
	for _, c := range calls {
		out = append(out, ContentBlock{
			Type:  "tool_use",
			ID:    c.ID,
			Name:  c.Function.Name,
			Input: json.RawMessage(c.Function.Arguments),
		})
	}
	return out
}
