package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

type responsesRequest struct {
	Model           string          `json:"model"`
	Input           []any           `json:"input"`
	MaxOutputTokens *int            `json:"max_output_tokens,omitempty"`
	Tools           json.RawMessage `json:"tools,omitempty"`
	ToolChoice      json.RawMessage `json:"tool_choice,omitempty"`
	Reasoning       map[string]any  `json:"reasoning,omitempty"`
	Store           bool            `json:"store"`
}

type responsesResponse struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Status string `json:"status"`
	Output []struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		InputDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details,omitempty"`
	} `json:"usage,omitempty"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details,omitempty"`
}

func usesResponsesAPI(model string) bool {
	return strings.HasPrefix(model, "gpt-5.6-")
}

func toResponsesRequest(chat OpenAIRequest) responsesRequest {
	req := responsesRequest{
		Model:           chat.Model,
		Input:           toResponsesInput(chat.Messages),
		MaxOutputTokens: chat.MaxTokens,
		Tools:           toResponsesTools(chat.Tools),
		ToolChoice:      toResponsesToolChoice(chat.ToolChoice),
		Store:           false,
	}
	if chat.ReasoningEffort != "" {
		req.Reasoning = map[string]any{"effort": chat.ReasoningEffort}
	}
	return req
}

func toResponsesInput(messages []OpenAIMessage) []any {
	var input []any
	for _, message := range messages {
		if message.Role == "tool" {
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": message.ToolCallID,
				"output": fmt.Sprint(message.Content),
			})
			continue
		}

		if message.Content != nil {
			content := message.Content
			if parts := message.contentParts(); len(parts) > 0 {
				converted := make([]any, 0, len(parts))
				for _, part := range parts {
					switch part.Type {
					case "text":
						converted = append(converted, map[string]any{"type": "input_text", "text": part.Text})
					case "image_url":
						if part.ImageURL != nil {
							converted = append(converted, map[string]any{"type": "input_image", "image_url": part.ImageURL.URL})
						}
					}
				}
				content = converted
			}
			input = append(input, map[string]any{"role": message.Role, "content": content})
		}
		for _, call := range message.ToolCalls {
			input = append(input, map[string]any{
				"type": "function_call", "call_id": call.ID,
				"name": call.Function.Name, "arguments": call.Function.Arguments,
			})
		}
	}
	return input
}

func toResponsesTools(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &tools) != nil {
		return raw
	}
	flat := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		flat = append(flat, map[string]any{
			"type": "function", "name": tool.Function.Name,
			"description": tool.Function.Description, "parameters": tool.Function.Parameters,
			"strict": false,
		})
	}
	out, _ := json.Marshal(flat)
	return out
}

func toResponsesToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var choice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &choice) == nil && choice.Type == "function" {
		out, _ := json.Marshal(map[string]any{"type": "function", "name": choice.Function.Name})
		return out
	}
	return raw
}

func createResponses(st *State, payload OpenAIRequest) (*http.Response, error) {
	reqPayload := toResponsesRequest(payload)
	body, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", copilotBaseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range copilotHeaders(st.getCopilotToken(), hasVision(payload.Messages)) {
		req.Header.Set(key, value)
	}
	req.Header.Set("X-Initiator", map[bool]string{true: "agent", false: "user"}[isAgentRequest(payload.Messages)])

	st.acquire()
	resp, err := http.DefaultClient.Do(req)
	st.release()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		return nil, rateLimitError{retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if resp.StatusCode >= 400 {
		responseBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Printf("[gopilot] Copilot responses rejected request (http %d): %s", resp.StatusCode, string(responseBody))
		return nil, fmt.Errorf("copilot responses: http %d: %s", resp.StatusCode, string(responseBody))
	}
	return resp, nil
}

func hasVision(messages []OpenAIMessage) bool {
	for _, message := range messages {
		for _, part := range message.contentParts() {
			if part.Type == "image_url" {
				return true
			}
		}
	}
	return false
}

func isAgentRequest(messages []OpenAIMessage) bool {
	for _, message := range messages {
		if message.Role == "assistant" || message.Role == "tool" {
			return true
		}
	}
	return false
}

func translateResponsesToAnthropic(response responsesResponse) AnthropicResponse {
	var content []ContentBlock
	stopReason := "end_turn"
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" && part.Text != "" {
					content = append(content, ContentBlock{Type: "text", Text: part.Text})
				}
			}
		case "function_call":
			stopReason = "tool_use"
			content = append(content, ContentBlock{
				Type: "tool_use", ID: item.CallID, Name: item.Name,
				Input: json.RawMessage(item.Arguments),
			})
		}
	}
	if response.Status == "incomplete" && response.IncompleteDetails != nil && response.IncompleteDetails.Reason == "max_output_tokens" {
		stopReason = "max_tokens"
	}
	usage := AnthropicUsage{}
	if response.Usage != nil {
		cached := 0
		if response.Usage.InputDetails != nil {
			cached = response.Usage.InputDetails.CachedTokens
			usage.CacheReadInputTokens = &cached
		}
		usage.InputTokens = response.Usage.InputTokens - cached
		usage.OutputTokens = response.Usage.OutputTokens
	}
	return AnthropicResponse{
		ID: response.ID, Type: "message", Role: "assistant", Model: response.Model,
		Content: content, StopReason: &stopReason, Usage: usage,
	}
}

func decodeResponses(resp *http.Response) (AnthropicResponse, error) {
	defer resp.Body.Close()
	var response responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return AnthropicResponse{}, err
	}
	return translateResponsesToAnthropic(response), nil
}

func writeAnthropicResponseSSE(w http.ResponseWriter, flusher http.Flusher, response AnthropicResponse) {
	writeSSE(w, flusher, sseEvent{"message_start", map[string]any{
		"type": "message_start", "message": map[string]any{
			"id": response.ID, "type": "message", "role": "assistant", "content": []any{},
			"model": response.Model, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": 0},
		},
	}})
	for index, block := range response.Content {
		switch block.Type {
		case "text":
			writeSSE(w, flusher, sseEvent{"content_block_start", map[string]any{
				"type": "content_block_start", "index": index,
				"content_block": map[string]any{"type": "text", "text": ""},
			}})
			writeSSE(w, flusher, sseEvent{"content_block_delta", map[string]any{
				"type": "content_block_delta", "index": index,
				"delta": map[string]any{"type": "text_delta", "text": block.Text},
			}})
		case "tool_use":
			writeSSE(w, flusher, sseEvent{"content_block_start", map[string]any{
				"type": "content_block_start", "index": index,
				"content_block": map[string]any{"type": "tool_use", "id": block.ID, "name": block.Name, "input": map[string]any{}},
			}})
			writeSSE(w, flusher, sseEvent{"content_block_delta", map[string]any{
				"type": "content_block_delta", "index": index,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": string(block.Input)},
			}})
		}
		writeSSE(w, flusher, sseEvent{"content_block_stop", map[string]any{"type": "content_block_stop", "index": index}})
	}
	writeSSE(w, flusher, sseEvent{"message_delta", map[string]any{
		"type": "message_delta", "delta": map[string]any{"stop_reason": response.StopReason, "stop_sequence": nil},
		"usage": map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens},
	}})
	writeSSE(w, flusher, sseEvent{"message_stop", map[string]any{"type": "message_stop"}})
}
