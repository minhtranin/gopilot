package main

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSemaphore_CapsConcurrency(t *testing.T) {
	st := newState("fake-token")
	var inFlight, maxSeen int32
	var wg sync.WaitGroup

	const totalCalls = 20
	for i := 0; i < totalCalls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st.acquire()
			n := atomic.AddInt32(&inFlight, 1)
			for {
				max := atomic.LoadInt32(&maxSeen)
				if n <= max || atomic.CompareAndSwapInt32(&maxSeen, max, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond) // hold the slot briefly, like a real HTTP call would
			atomic.AddInt32(&inFlight, -1)
			st.release()
		}()
	}
	wg.Wait()

	if maxSeen > maxConcurrentCopilotRequests {
		t.Fatalf("semaphore let %d requests run at once, want <= %d — this is exactly the burst that 429'd live",
			maxSeen, maxConcurrentCopilotRequests)
	}
	if maxSeen < 1 {
		t.Fatal("nothing ran at all")
	}
}

func TestSemaphore_ReleasesOnEveryPath(t *testing.T) {
	// If release() were ever skipped, the semaphore permanently shrinks —
	// a slow leak that would eventually deadlock every request. Run more
	// acquire/release cycles than the semaphore's capacity and confirm it
	// never blocks forever.
	st := newState("fake-token")
	done := make(chan struct{})
	go func() {
		for i := 0; i < maxConcurrentCopilotRequests*10; i++ {
			st.acquire()
			st.release()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("semaphore deadlocked — a release() path is missing somewhere")
	}
}

func TestBackoffFor_RateLimitError_UsesRetryAfter(t *testing.T) {
	err := rateLimitError{retryAfter: 7 * time.Second}
	got := backoffFor(err)
	if got != 7*time.Second {
		t.Fatalf("want 7s (Copilot's actual Retry-After), got %s", got)
	}
}

func TestBackoffFor_WrappedRateLimitError_StillUnwraps(t *testing.T) {
	err := fmt_errorf_wrap(rateLimitError{retryAfter: 2 * time.Second})
	got := backoffFor(err)
	if got != 2*time.Second {
		t.Fatalf("errors.As must see through wrapping, got %s", got)
	}
}

func TestBackoffFor_OtherError_UsesFlatFallback(t *testing.T) {
	got := backoffFor(errors.New("connection refused"))
	if got != 400*time.Millisecond {
		t.Fatalf("non-ratelimit errors should use the flat fallback, got %s", got)
	}
}

func TestParseRetryAfter_ValidSeconds(t *testing.T) {
	if got := parseRetryAfter("5"); got != 5*time.Second {
		t.Fatalf("want 5s, got %s", got)
	}
}

func TestParseRetryAfter_MissingOrInvalid_DefaultsSafely(t *testing.T) {
	for _, h := range []string{"", "not-a-number", "0", "-1"} {
		got := parseRetryAfter(h)
		if got <= 0 {
			t.Fatalf("header %q produced non-positive wait %s — would busy-loop retries", h, got)
		}
	}
}

func fmt_errorf_wrap(err error) error {
	return &wrapped{err}
}

type wrapped struct{ err error }

func (w *wrapped) Error() string { return "wrapped: " + w.err.Error() }
func (w *wrapped) Unwrap() error { return w.err }
