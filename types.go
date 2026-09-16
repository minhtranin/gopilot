package main

import (
	"bytes"
	"encoding/json"
)

// ---- Anthropic wire format (what Claude Code sends to /v1/messages) ----

type AnthropicRequest struct {
	Model         string          `json:"model"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
	System        json.RawMessage `json:"system,omitempty"`
	Messages      []AnthropicMsg  `json:"messages"`
	Tools         json.RawMessage `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Metadata      *struct {
		UserID string `json:"user_id,omitempty"`
	} `json:"metadata,omitempty"`
}

type AnthropicMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ContentBlock is a flattened union of every Anthropic content-block shape
// (text, thinking, tool_use, tool_result, image) — one struct, unused
// fields just stay zero, same trick the JS port takes with plain objects.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // tool_result: string or []ContentBlock
	Source    *ImageSource    `json:"source,omitempty"`
}

type ImageSource struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// decodeBlocks normalizes Anthropic's "string OR []block" content shape
// into a single []ContentBlock — every message field on both request and
// response sides can legally be either.
func decodeBlocks(raw json.RawMessage) []ContentBlock {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '"' {
		var s string
		json.Unmarshal(raw, &s)
		return []ContentBlock{{Type: "text", Text: s}}
	}
	var blocks []ContentBlock
	json.Unmarshal(raw, &blocks)
	return blocks
}

// ---- Anthropic response / SSE event shapes (what we send back) ----

type AnthropicResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []ContentBlock `json:"content"`
	StopReason   *string        `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        AnthropicUsage `json:"usage"`
}

type AnthropicUsage struct {
	InputTokens          int  `json:"input_tokens"`
	OutputTokens         int  `json:"output_tokens"`
	CacheReadInputTokens *int `json:"cache_read_input_tokens,omitempty"`
}

// ---- OpenAI wire format (what Copilot's backend actually speaks) ----

type OpenAIRequest struct {
	Model           string          `json:"model"`
	Messages        []OpenAIMessage `json:"messages"`
	MaxTokens       *int            `json:"max_tokens,omitempty"`
	Stop            []string        `json:"stop,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	User            string          `json:"user,omitempty"`
	Tools           json.RawMessage `json:"tools,omitempty"`
	ToolChoice      json.RawMessage `json:"tool_choice,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    interface{}      `json:"content"` // string or []OpenAIContentPart
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

func (m OpenAIMessage) contentParts() []OpenAIContentPart {
	parts, _ := m.Content.([]OpenAIContentPart)
	return parts
}

type OpenAIContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *OpenAIImgURL `json:"image_url,omitempty"`
}

type OpenAIImgURL struct {
	URL string `json:"url"`
}

type OpenAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function OpenAIToolCallFunc `json:"function"`
}

type OpenAIToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type OpenAIResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   *OpenAIUsage   `json:"usage,omitempty"`
}

type OpenAIChoice struct {
	Message      OpenAIRespMessage `json:"message"`
	FinishReason *string           `json:"finish_reason"`
}

type OpenAIRespMessage struct {
	Content   json.RawMessage  `json:"content"` // string or null
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	PromptDetails    *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
}

// ---- streamed chunk shape ----

type OpenAIStreamChunk struct {
	ID      string               `json:"id"`
	Model   string               `json:"model"`
	Choices []OpenAIStreamChoice `json:"choices"`
	Usage   *OpenAIUsage         `json:"usage,omitempty"`
}

type OpenAIStreamChoice struct {
	Delta        OpenAIStreamDelta `json:"delta"`
	FinishReason *string           `json:"finish_reason"`
}

type OpenAIStreamDelta struct {
	Content   string                 `json:"content,omitempty"`
	ToolCalls []OpenAIStreamToolCall `json:"tool_calls,omitempty"`
}

type OpenAIStreamToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id,omitempty"`
	Function OpenAIToolCallFunc `json:"function,omitempty"`
}
