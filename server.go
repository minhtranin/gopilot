package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

func newServer(st *State) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("gopilot running"))
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		body, err := fetchModels(st)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.Write(body)
	})
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		handleMessages(st, w, r)
	})
	return mux
}

func handleMessages(st *State, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	log.Printf("[gopilot] incoming /v1/messages (%d bytes): %s", len(body), truncate(body, 4000))

	var anthropicReq AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}

	openaiReq := translateToOpenAI(anthropicReq)
	if usesResponsesAPI(openaiReq.Model) {
		handleResponsesModel(st, w, anthropicReq, openaiReq)
		return
	}

	if !anthropicReq.Stream {
		// The main non-stream caller in practice is `ocr` reviewing a PR —
		// one call per changed file, all fired in parallel. Confirmed live
		// (2026-08-25): 51 landed in 10s for one PR, swamped Copilot's rate
		// limit, HTTP 429 on most. createChatCompletions' own semaphore
		// serializes the send side; this retry+backoff covers whichever
		// ones still got 429'd before their turn.
		var resp *http.Response
		var err error
		const maxNonStreamAttempts = 4
		for attempt := 1; attempt <= maxNonStreamAttempts; attempt++ {
			resp, err = createChatCompletions(st, openaiReq)
			if err == nil {
				break
			}
			wait := backoffFor(err)
			log.Printf("[gopilot] non-stream attempt %d/%d failed: %v (waiting %s)",
				attempt, maxNonStreamAttempts, err, wait)
			if attempt < maxNonStreamAttempts {
				time.Sleep(wait)
			}
		}
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		var openaiResp OpenAIResponse
		if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
			http.Error(w, "bad copilot response: "+err.Error(), 502)
			return
		}
		out := translateToAnthropic(openaiResp)
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(out)
		return
	}

	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	streamWithRetry(func() (*http.Response, error) {
		return createChatCompletions(st, openaiReq)
	}, w, flusher)
}

func handleResponsesModel(st *State, w http.ResponseWriter, anthropicReq AnthropicRequest, openaiReq OpenAIRequest) {
	var response AnthropicResponse
	var err error
	const maxAttempts = 4
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var upstream *http.Response
		upstream, err = createResponses(st, openaiReq)
		if err == nil {
			response, err = decodeResponses(upstream)
		}
		if err == nil {
			break
		}
		wait := backoffFor(err)
		log.Printf("[gopilot] responses attempt %d/%d failed: %v (waiting %s)", attempt, maxAttempts, err, wait)
		if attempt < maxAttempts {
			time.Sleep(wait)
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if !anthropicReq.Stream {
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	writeAnthropicResponseSSE(w, flusher, response)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}
