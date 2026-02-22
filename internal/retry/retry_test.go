package retry

import (
	"fmt"
	"testing"
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
	err := Do(func() error {
		calls++
		if calls <= 2 {
			return &HTTPError{StatusCode: 500, Message: "server error"}
		}
		return nil
	}, 3)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", calls)
	}
}

func TestDo_AllAttemptsExhausted(t *testing.T) {
	calls := 0
	err := Do(func() error {
		calls++
		return &HTTPError{StatusCode: 500, Message: "server error"}
	}, 3)

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
