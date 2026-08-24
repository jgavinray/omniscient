package llm

import (
	"context"
	"strings"
	"time"

	"github.com/jgavinray/omniscient/internal/models"
	"github.com/jgavinray/omniscient/internal/retry"
)

// cleanResponse strips markdown code fences from LLM output.
func cleanResponse(s string) string {
	return models.StripCodeFences(s)
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
