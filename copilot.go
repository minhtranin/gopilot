package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

// rateLimitError carries how long Copilot said to wait, so callers can
// back off the actual amount instead of a guess. Copilot's Retry-After is
// in seconds; default covers the rare case it's absent.
type rateLimitError struct {
	retryAfter time.Duration
}

func (e rateLimitError) Error() string {
	return fmt.Sprintf("copilot rate limited, retry after %s", e.retryAfter)
}

func parseRetryAfter(h string) time.Duration {
	if secs, err := strconv.Atoi(h); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 3 * time.Second
}

// Exact values the real VS Code Copilot Chat extension sends. Copilot's
// backend gates on these — a wrong/missing editor-version or
// copilot-integration-id gets a 403 before your token is even checked.
const (
	copilotVersion      = "0.26.7"
	editorPluginVersion = "copilot-chat/" + copilotVersion
	userAgent           = "GitHubCopilotChat/" + copilotVersion
	githubAPIVersion    = "2025-04-01"
	vsCodeVersion       = "1.104.3"
	copilotBaseURL      = "https://api.githubcopilot.com"
)

func githubHeaders(githubToken string) map[string]string {
	return map[string]string{
		"content-type":                        "application/json",
		"accept":                              "application/json",
		"authorization":                       "token " + githubToken,
		"editor-version":                      "vscode/" + vsCodeVersion,
		"editor-plugin-version":               editorPluginVersion,
		"user-agent":                          userAgent,
		"x-github-api-version":                githubAPIVersion,
		"x-vscode-user-agent-library-version": "electron-fetch",
	}
}

func requestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func copilotHeaders(copilotToken string, vision bool) map[string]string {
	h := map[string]string{
		"Authorization":                       "Bearer " + copilotToken,
		"content-type":                        "application/json",
		"copilot-integration-id":              "vscode-chat",
		"editor-version":                      "vscode/" + vsCodeVersion,
		"editor-plugin-version":               editorPluginVersion,
		"user-agent":                          userAgent,
		"openai-intent":                       "conversation-panel",
		"x-github-api-version":                githubAPIVersion,
		"x-request-id":                        requestID(),
		"x-vscode-user-agent-library-version": "electron-fetch",
	}
	if vision {
		h["copilot-vision-request"] = "true"
	}
	return h
}

// createChatCompletions posts the already-OpenAI-shaped payload to Copilot
// and returns the raw HTTP response — caller decides stream vs non-stream
// handling, since that's a property of the request, not this call.
func createChatCompletions(st *State, payload OpenAIRequest) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	isAgentCall := false
	for _, m := range payload.Messages {
		if m.Role == "assistant" || m.Role == "tool" {
			isAgentCall = true
			break
		}
	}
	vision := false
	for _, m := range payload.Messages {
		for _, p := range m.contentParts() {
			if p.Type == "image_url" {
				vision = true
			}
		}
	}
	req, err := http.NewRequest("POST", copilotBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range copilotHeaders(st.getCopilotToken(), vision) {
		req.Header.Set(k, v)
	}
	req.Header.Set("X-Initiator", map[bool]string{true: "agent", false: "user"}[isAgentCall])

	// Confirmed live (2026-08-25): a single PR review fires one call per
	// changed file — 51 landed in 10s for one PR — and Copilot's rate limit
	// can't absorb that. Serializing the actual send is what turns "most of
	// a burst 429s" into "the burst just takes longer, all of it succeeds".
	st.acquire()
	resp, err := http.DefaultClient.Do(req)
	st.release()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 429 {
		resp.Body.Close()
		return nil, rateLimitError{retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Printf("[gopilot] copilot rejected request (http %d)\nheaders sent: %v\nrequest body: %s\nresponse body: %s",
			resp.StatusCode, req.Header, string(body), string(b))
		return nil, fmt.Errorf("copilot chat/completions: http %d: %s", resp.StatusCode, string(b))
	}
	return resp, nil
}

func fetchModels(st *State) (json.RawMessage, error) {
	req, _ := http.NewRequest("GET", copilotBaseURL+"/models", nil)
	for k, v := range copilotHeaders(st.getCopilotToken(), false) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
