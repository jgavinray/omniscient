package retry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestIsTransient_429(t *testing.T) {
	err := &HTTPError{StatusCode: 429, Message: "rate limited"}
	if !IsTransient(err) {
		t.Error("expected 429 to be transient")
	}
}

func TestIsTransient_500(t *testing.T) {
	err := &HTTPError{StatusCode: 500, Message: "internal server error"}
	if !IsTransient(err) {
		t.Error("expected 500 to be transient")
	}
}

func TestIsTransient_502(t *testing.T) {
	err := &HTTPError{StatusCode: 502, Message: "bad gateway"}
	if !IsTransient(err) {
		t.Error("expected 502 to be transient")
	}
}

func TestIsTransient_401(t *testing.T) {
	err := &HTTPError{StatusCode: 401, Message: "unauthorized"}
	if IsTransient(err) {
		t.Error("expected 401 to NOT be transient")
	}
}

func TestIsTransient_NonHTTPError(t *testing.T) {
	err := fmt.Errorf("some non-HTTP error")
	if IsTransient(err) {
		t.Error("expected non-HTTP error to NOT be transient")
	}
}

func TestDo_SuccessOnFirstAttempt(t *testing.T) {
	calls := 0
	err := Do(func() error {
		calls++
		return nil
	}, 3)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestDo_PermanentError_NoRetry(t *testing.T) {
	calls := 0
	err := Do(func() error {
		calls++
		return &HTTPError{StatusCode: 401, Message: "unauthorized"}
	}, 3)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry for permanent error), got %d", calls)
	}
}

func TestDo_TransientThenSuccess(t *testing.T) {
	calls := 0
	err := doWithSleeper(context.Background(), func() error {
		calls++
		if calls <= 2 {
			return &HTTPError{StatusCode: 500, Message: "server error"}
		}
		return nil
	}, 3, func(ctx context.Context, d time.Duration) error { return nil }, func(d time.Duration) time.Duration { return d })

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", calls)
	}
}

func TestDo_AllAttemptsExhausted(t *testing.T) {
	calls := 0
	err := doWithSleeper(context.Background(), func() error {
		calls++
		return &HTTPError{StatusCode: 500, Message: "server error"}
	}, 3, func(ctx context.Context, d time.Duration) error { return nil }, func(d time.Duration) time.Duration { return d })

	if err == nil {
		t.Fatal("expected error after all attempts exhausted, got nil")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestHTTPError_Error(t *testing.T) {
	err := &HTTPError{StatusCode: 503, Message: "service unavailable"}
	expected := "HTTP 503: service unavailable"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestIsTransient_403(t *testing.T) {
	err := &HTTPError{StatusCode: 403, Message: "forbidden"}
	if IsTransient(err) {
		t.Error("expected 403 to NOT be transient")
	}
}

func TestIsTransient_WrappedHTTPError(t *testing.T) {
	wrapped := fmt.Errorf("wrapped: %w", &HTTPError{StatusCode: 500, Message: "internal server error"})
	if !IsTransient(wrapped) {
		t.Error("expected wrapped 500 to be transient")
	}
}

func TestDoWithSleeper_UsesRetryAfterForWrappedHTTPError(t *testing.T) {
	var sleptDuration time.Duration
	sleeper := func(ctx context.Context, d time.Duration) error {
		sleptDuration = d
		return nil
	}
	jitter := func(d time.Duration) time.Duration { return d } // identity jitter

	calls := 0
	fn := func() error {
		calls++
		if calls == 1 {
			return fmt.Errorf("wrapped: %w", &HTTPError{StatusCode: 429, Message: "rate limited", RetryAfter: 50 * time.Millisecond})
		}
		return nil
	}

	err := doWithSleeper(context.Background(), fn, 3, sleeper, jitter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sleptDuration != 50*time.Millisecond {
		t.Errorf("expected sleeper to receive 50ms, got %v", sleptDuration)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestDoWithSleeper_StopsOnContextCancellation(t *testing.T) {
	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())

	fn := func() error {
		callCount++
		return &HTTPError{StatusCode: 500, Message: "server error"}
	}

	sleeper := func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}

	err := doWithSleeper(ctx, fn, 3, sleeper, func(d time.Duration) time.Duration { return d })
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify the returned error is an *HTTPError (not the wrapped "all attempts failed" error)
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Errorf("expected returned error to be *HTTPError, got %T: %v", err, err)
	}

	// Should have called the function once, then slept once, then returned on context cancellation
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}

func TestDoWithSleeper_401And403DoNotSleep(t *testing.T) {
	for _, code := range []int{401, 403} {
		t.Run(fmt.Sprintf("%d", code), func(t *testing.T) {
			called := false
			fn := func() error {
				called = true
				return &HTTPError{StatusCode: code, Message: "error"}
			}

			sleeper := func(ctx context.Context, d time.Duration) error {
				t.Error("sleeper should not be called for non-transient errors")
				return nil
			}

			_ = doWithSleeper(context.Background(), fn, 3, sleeper, func(d time.Duration) time.Duration { return d })
			if !called {
				t.Error("function should have been called")
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantD        time.Duration
		wantOk       bool
		wantPositive bool
	}{
		{"seconds", "120", 120 * time.Second, true, false},
		{"zero", "0", 0, true, false},
		{"negative", "-1", 0, false, false},
		{"empty", "", 0, false, false},
		{"whitespace", "  ", 0, false, false},
		{"invalid", "abc", 0, false, false},
		{"future HTTP date", time.Now().Add(time.Hour).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"), 0, true, true},
		{"past HTTP date", time.Now().Add(-time.Hour).UTC().Format(time.RFC1123), 0, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, ok := ParseRetryAfter(tt.input)
			if ok != tt.wantOk {
				t.Errorf("got ok=%v, want %v", ok, tt.wantOk)
			}
			if tt.wantPositive {
				if d <= 0 {
					t.Errorf("got duration %v, want positive", d)
				}
			} else {
				if d != tt.wantD {
					t.Errorf("got duration %v, want %v", d, tt.wantD)
				}
			}
		})
	}
}
