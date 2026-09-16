package main

import "sync"

// maxConcurrentCopilotRequests caps how many requests gopilot has in flight
// to Copilot at once. Confirmed live (2026-08-25): `ocr` reviewing a PR
// fires one call per changed file — 51 landed within 10s for one PR review
// — and Copilot's per-account rate limit can't absorb that, so most came
// back HTTP 429 and even retries got 429'd again since the burst never
// thinned out. Serializing here trades latency for actually succeeding.
const maxConcurrentCopilotRequests = 4

type State struct {
	githubToken string

	mu           sync.RWMutex
	copilotToken string

	sem chan struct{}
}

func newState(githubToken string) *State {
	return &State{
		githubToken: githubToken,
		sem:         make(chan struct{}, maxConcurrentCopilotRequests),
	}
}

func (s *State) acquire() { s.sem <- struct{}{} }
func (s *State) release() { <-s.sem }

func (s *State) setCopilotToken(t string) {
	s.mu.Lock()
	s.copilotToken = t
	s.mu.Unlock()
}

func (s *State) getCopilotToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.copilotToken
}
