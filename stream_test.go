package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseBody(lines ...string) *http.Response {
	body := strings.Join(lines, "\n") + "\n"
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}

// exact shape captured live from Copilot: reasoning-only, then error, no content ever.
func emptyErrorBody() *http.Response {
	return sseBody(
		`data: {"choices":[{"index":0,"delta":{"content":null,"role":"assistant"}}],"id":"x","model":"gemini-3.5-flash"}`,
		`data: {"choices":[{"finish_reason":"error","index":0,"delta":{"content":null}}],"id":"x","model":"gemini-3.5-flash"}`,
		`data: [DONE]`,
	)
}

func goodTextBody() *http.Response {
	return sseBody(
		`data: {"choices":[{"index":0,"delta":{"content":"Hi","role":"assistant"}}],"id":"x","model":"m"}`,
		`data: {"choices":[{"index":0,"delta":{"content":" there"}}],"id":"x","model":"m"}`,
		`data: {"choices":[{"finish_reason":"stop","index":0,"delta":{}}],"id":"x","model":"m","usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`data: [DONE]`,
	)
}

func partialThenErrorBody() *http.Response {
	return sseBody(
		`data: {"choices":[{"index":0,"delta":{"content":"partial","role":"assistant"}}],"id":"x","model":"m"}`,
		`data: {"choices":[{"finish_reason":"error","index":0,"delta":{"content":null}}],"id":"x","model":"m"}`,
		`data: [DONE]`,
	)
}

func run(t *testing.T, fetch func() (*http.Response, error)) string {
	t.Helper()
	rec := httptest.NewRecorder()
	streamWithRetry(fetch, rec, nil)
	return rec.Body.String()
}

func TestRetry_SucceedsAfterTwoEmptyErrors(t *testing.T) {
	calls := 0
	out := run(t, func() (*http.Response, error) {
		calls++
		switch calls {
		case 1, 2:
			return emptyErrorBody(), nil
		case 3:
			return goodTextBody(), nil
		default:
			t.Fatalf("should not be called a 4th time, got call %d", calls)
			return nil, nil
		}
	})
	if calls != 3 {
		t.Fatalf("want 3 calls (2 failed + 1 success), got %d", calls)
	}
	if !strings.Contains(out, `"text":"Hi"`) || !strings.Contains(out, `"text":" there"`) {
		t.Fatalf("expected the real streamed text to reach the client, got:\n%s", out)
	}
	if strings.Contains(out, "gopilot: Copilot backend") {
		t.Fatalf("should NOT show the fallback message when a retry succeeded, got:\n%s", out)
	}
}

func TestRetry_ExhaustsAndFallsBackToVisibleError(t *testing.T) {
	calls := 0
	out := run(t, func() (*http.Response, error) {
		calls++
		return emptyErrorBody(), nil
	})
	if calls != maxStreamAttempts {
		t.Fatalf("want exactly %d attempts, got %d", maxStreamAttempts, calls)
	}
	if !strings.Contains(out, "gopilot: Copilot backend") {
		t.Fatalf("expected the fallback error message after exhausting retries, got:\n%s", out)
	}
	if !strings.Contains(out, `"stop_reason":"end_turn"`) {
		t.Fatalf("fallback must use a stop_reason Claude Code understands, got:\n%s", out)
	}
}

func TestRetry_FirstAttemptSucceeds_NoWastedCalls(t *testing.T) {
	calls := 0
	out := run(t, func() (*http.Response, error) {
		calls++
		return goodTextBody(), nil
	})
	if calls != 1 {
		t.Fatalf("want exactly 1 call on the happy path, got %d", calls)
	}
	if !strings.Contains(out, `"text":"Hi"`) {
		t.Fatalf("missing streamed content:\n%s", out)
	}
}

// The one case retry must NOT touch: real content already reached the
// client. Retrying here would double-send content Claude Code already saw.
func TestRetry_PartialContentThenError_DoesNotRetry(t *testing.T) {
	calls := 0
	out := run(t, func() (*http.Response, error) {
		calls++
		return partialThenErrorBody(), nil
	})
	if calls != 1 {
		t.Fatalf("must not retry once real content was sent — want 1 call, got %d", calls)
	}
	if !strings.Contains(out, `"text":"partial"`) {
		t.Fatalf("the partial content that WAS sent must still be in the output:\n%s", out)
	}
	if strings.Contains(out, "gopilot: Copilot backend") {
		t.Fatalf("must not append the fallback message on top of real partial content:\n%s", out)
	}
}

func TestRetry_NetworkErrorOnFetch_Retries(t *testing.T) {
	calls := 0
	out := run(t, func() (*http.Response, error) {
		calls++
		if calls < 3 {
			return nil, errFake{}
		}
		return goodTextBody(), nil
	})
	if calls != 3 {
		t.Fatalf("want 3 calls (2 network failures + 1 success), got %d", calls)
	}
	if !strings.Contains(out, `"text":"Hi"`) {
		t.Fatalf("expected success after retrying network errors:\n%s", out)
	}
}

type errFake struct{}

func (errFake) Error() string { return "connection refused" }
