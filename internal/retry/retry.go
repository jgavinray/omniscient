package retry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxErrBodyBytes = 512

// TruncateBody shortens a response body for safe inclusion in error messages.
func TruncateBody(body string) string {
	if len(body) > maxErrBodyBytes {
		return body[:maxErrBodyBytes] + "... [truncated]"
	}
	return body
}

// HTTPError represents an HTTP error with a status code, allowing the retry
// logic to distinguish between transient and permanent failures.
type HTTPError struct {
	StatusCode int
	Message    string
	// RetryAfter is populated when the HTTP response contains a Retry-After
	// header that could be parsed. It is zero when no header is present or
	// when the header value could not be parsed.
	RetryAfter time.Duration
}

// Error implements the error interface for HTTPError.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, TruncateBody(e.Message))
}

// IsTransient checks whether the error is a transient HTTP error that should
// be retried. Returns true for HTTP 429 (rate limit) and 5xx (server errors).
// Permanent errors such as 401 and 403 are NOT considered transient.
func IsTransient(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.StatusCode == 429 || he.StatusCode >= 500
	}
	return false
}

// ParseRetryAfter parses a Retry-After header value. It supports:
//   - A decimal integer (seconds, e.g. "120")
//   - An HTTP-date string (e.g. "Wed, 21 Oct 2025 07:28:00 GMT")
//
// Returns the parsed duration and true on success, or 0 and false on failure.
// Returns false for empty strings, invalid values, negative seconds, or
// past/zero HTTP-date durations.
func ParseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	// Try integer seconds first.
	if secs, err := strconv.ParseInt(value, 10, 64); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second, true
	}

	// Try HTTP-date format.
	if t, err := http.ParseTime(value); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d, true
		}
		return 0, false
	}

	return 0, false
}

// jitter adds a small bounded random offset (0..100ms) to a duration.
func jitter(d time.Duration) time.Duration {
	if d == 0 {
		return 0
	}
	return d + time.Duration(rand.Intn(101))*time.Millisecond
}

// Do executes fn up to maxAttempts times with exponential backoff for
// transient errors. Non-transient errors are returned immediately. The backoff
// schedule is 1s, 2s, 4s, etc.
//
// Do delegates to DoContext using context.Background() for compatibility.
func Do(fn func() error, maxAttempts int) error {
	return DoContext(context.Background(), fn, maxAttempts)
}

// DoContext executes fn up to maxAttempts times with exponential backoff for
// transient errors. The context is checked before each sleep so that callers
// can cancel the retry loop. Non-transient errors are returned immediately.
//
// The backoff schedule is 1s, 2s, 4s, etc. For HTTP 429 responses that carry
// a parseable Retry-After header, the Retry-After value is used as the delay
// (with jitter) instead of the exponential backoff.
//
// For 401 and 403 errors, the loop exits immediately (no retry).
func DoContext(ctx context.Context, fn func() error, maxAttempts int) error {
	return doWithSleeper(ctx, fn, maxAttempts, defaultSleeper, jitter)
}

// defaultSleeper waits for the given duration using a timer, returning
// ctx.Err() promptly if the context is canceled.
func defaultSleeper(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// doWithSleeper is the internal retry loop with a pluggable sleeper and jitter
// function for testability.
func doWithSleeper(ctx context.Context, fn func() error, maxAttempts int, sleep func(context.Context, time.Duration) error, addJitter func(time.Duration) time.Duration) error {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if !IsTransient(lastErr) {
			return lastErr
		}

		if attempt < maxAttempts-1 {
			var backoff time.Duration
			var useRetryAfter bool

			var he *HTTPError
			if errors.As(lastErr, &he) {
				if he.RetryAfter > 0 {
					backoff = he.RetryAfter
					useRetryAfter = true
				}
			}

			if !useRetryAfter {
				backoff = time.Duration(math.Pow(2, float64(attempt))) * time.Second
			}

			backoff = addJitter(backoff)

			slog.Warn("transient error, retrying",
				"attempt", attempt+1,
				"max_attempts", maxAttempts,
				"backoff", backoff.String(),
				"error", lastErr.Error(),
			)

			if err := sleep(ctx, backoff); err != nil {
				return lastErr
			}
		}
	}
	return fmt.Errorf("all %d attempts failed, last error: %w", maxAttempts, lastErr)
}
