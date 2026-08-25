package llm

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jgavinray/omniscient/internal/retry"
)

// withImmediateRetries replaces retryableCtx with an immediate (no-sleep)
// version for use in tests. It restores the original on cleanup.
func withImmediateRetries(t *testing.T) func() {
	t.Helper()
	old := retryableCtx
	retryableCtx = func(ctx context.Context, fn func() error, maxAttempts int) error {
		var lastErr error
		for attempt := 0; attempt < maxAttempts; attempt++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = fn()
			if lastErr == nil {
				return nil
			}
			if !isTransient(lastErr) {
				return lastErr
			}
		}
		return fmt.Errorf("all %d attempts failed, last error: %w", maxAttempts, lastErr)
	}
	t.Cleanup(func() {
		retryableCtx = old
	})
	return func() {
		retryableCtx = old
	}
}

func TestCleanResponse_Plain(t *testing.T) {
	input := `{"meeting_type": "standup"}`
	got := cleanResponse(input)
	if got != input {
		t.Errorf("expected %q, got %q", input, got)
	}
}

func TestCleanResponse_JsonFence(t *testing.T) {
	input := "```json\n{\"meeting_type\": \"standup\"}\n```"
	expected := `{"meeting_type": "standup"}`
	got := cleanResponse(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestCleanResponse_PlainFence(t *testing.T) {
	input := "```\n{\"meeting_type\": \"standup\"}\n```"
	expected := `{"meeting_type": "standup"}`
	got := cleanResponse(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestCleanResponse_MarkdownFence(t *testing.T) {
	input := "```markdown\n---\ndate: 2026-02-21\n---\n## Summary\n```"
	expected := "---\ndate: 2026-02-21\n---\n## Summary"
	got := cleanResponse(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestCleanResponse_Whitespace(t *testing.T) {
	input := "  \n  {\"meeting_type\": \"standup\"}  \n  "
	expected := `{"meeting_type": "standup"}`
	got := cleanResponse(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestBuildClassifyPrompt(t *testing.T) {
	template := "Classify: {{TEMPLATE_KEYS}}\n\nPreview: {{TRANSCRIPT_PREVIEW}}"
	keys := []string{"engineering", "customer_success", "planning"}
	preview := "Alice: Let's discuss the sprint."

	result := buildClassifyPrompt(template, preview, keys)

	if !strings.Contains(result, "engineering, customer_success, planning") {
		t.Error("result should contain template keys")
	}
	if !strings.Contains(result, preview) {
		t.Error("result should contain transcript preview")
	}
}

func TestBuildExtractionPrompt(t *testing.T) {
	template := "Extract from:\n{{TRANSCRIPT}}\n\nDone."
	transcript := "Alice: Hello\nBob: Hi"

	result := buildExtractionPrompt(template, transcript)

	if !strings.Contains(result, transcript) {
		t.Error("result should contain transcript")
	}
	if strings.Contains(result, "{{TRANSCRIPT}}") {
		t.Error("result should not contain raw placeholder")
	}
}

func TestIsTransient_429(t *testing.T) {
	err := &retry.HTTPError{StatusCode: 429, Message: "rate limited"}
	if !retry.IsTransient(err) {
		t.Error("expected 429 to be transient")
	}
}

func TestIsTransient_500(t *testing.T) {
	err := &retry.HTTPError{StatusCode: 500, Message: "internal server error"}
	if !retry.IsTransient(err) {
		t.Error("expected 500 to be transient")
	}
}

func TestIsTransient_401(t *testing.T) {
	err := &retry.HTTPError{StatusCode: 401, Message: "unauthorized"}
	if retry.IsTransient(err) {
		t.Error("expected 401 to NOT be transient")
	}
}

func TestIsTransient_NonHTTPError(t *testing.T) {
	err := fmt.Errorf("some non-HTTP error")
	if retry.IsTransient(err) {
		t.Error("expected non-HTTP error to NOT be transient")
	}
}
