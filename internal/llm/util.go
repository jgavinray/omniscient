package llm

import (
	"context"
	"strings"
	"time"

	"github.com/jgavinray/omniscient/internal/models"
	"github.com/jgavinray/omniscient/internal/retry"
)

// httpError represents an HTTP error with a status code, allowing the retry
// logic to distinguish between transient and permanent failures.
type httpError struct {
	StatusCode int
	Message    string
}

// Error implements the error interface for httpError.
func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// isTransient checks whether an error is a transient HTTP error that should
// be retried. Returns true for HTTP 429 (rate limit) and 5xx (server errors).
func isTransient(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.StatusCode == 429 || he.StatusCode >= 500
	}
	return false
}

// retryable executes fn up to maxAttempts times with exponential backoff for
// transient errors. Non-transient errors are returned immediately. The backoff
// schedule is 1s, 2s, 4s, etc.
func retryable(fn func() error, maxAttempts int) error {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if !isTransient(lastErr) {
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

// cleanResponse strips markdown code fences from LLM output. LLMs often
// wrap responses in ```json ... ``` or ```markdown ... ``` blocks.
func cleanResponse(s string) string {
	s = strings.TrimSpace(s)

	// Remove leading ```json or ```markdown or ``` fence.
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```markdown") {
		s = strings.TrimPrefix(s, "```markdown")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}

	// Remove trailing ``` fence.
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}

	return strings.TrimSpace(s)
}

// buildClassifyPrompt substitutes placeholders in the classify prompt template.
func buildClassifyPrompt(classifyTemplate, transcriptPreview string, templateKeys []string) string {
	s := strings.ReplaceAll(classifyTemplate, "{{TEMPLATE_KEYS}}", strings.Join(templateKeys, ", "))
	s = strings.ReplaceAll(s, "{{TRANSCRIPT_PREVIEW}}", transcriptPreview)
	return s
}

// buildExtractionPrompt substitutes the transcript placeholder in an extraction prompt.
func buildExtractionPrompt(extractionTemplate, transcript string) string {
	return strings.ReplaceAll(extractionTemplate, "{{TRANSCRIPT}}", transcript)
}

// Re-export retry types and functions so existing llm package code
// that references these can still work internally.

type httpError = retry.HTTPError

func isTransient(err error) bool {
	return retry.IsTransient(err)
}

func retryable(fn func() error, maxAttempts int) error {
	return retry.Do(fn, maxAttempts)
}

var retryableCtx = func(ctx context.Context, fn func() error, maxAttempts int) error {
	return retry.DoContext(ctx, fn, maxAttempts)
}

func truncateBody(body string) string {
	return retry.TruncateBody(body)
}

func parseRetryAfter(value string) (time.Duration, bool) {
	return retry.ParseRetryAfter(value)
}
