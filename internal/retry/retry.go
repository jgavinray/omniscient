package retry

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"
)

// HTTPError represents an HTTP error with a status code, allowing the retry
// logic to distinguish between transient and permanent failures.
type HTTPError struct {
	StatusCode int
	Message    string
}

// Error implements the error interface for HTTPError.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// IsTransient checks whether an error is a transient HTTP error that should
// be retried. Returns true for HTTP 429 (rate limit) and 5xx (server errors).
func IsTransient(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.StatusCode == 429 || he.StatusCode >= 500
	}
	return false
}

// Do executes fn up to maxAttempts times with exponential backoff for
// transient errors. Non-transient errors are returned immediately. The backoff
// schedule is 1s, 2s, 4s, etc.
func Do(fn func() error, maxAttempts int) error {
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
			backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
			slog.Warn("transient error, retrying",
				"attempt", attempt+1,
				"max_attempts", maxAttempts,
				"backoff", backoff.String(),
				"error", lastErr.Error(),
			)
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("all %d attempts failed, last error: %w", maxAttempts, lastErr)
}
