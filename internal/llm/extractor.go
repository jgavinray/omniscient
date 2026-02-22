package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/jgavinray/omniscient/internal/config"
)

// Extractor defines the interface for LLM-based transcript processing.
// Classify picks a meeting type; Extract runs the extraction prompt and returns raw text.
type Extractor interface {
	Classify(ctx context.Context, transcriptPreview string, templateKeys []string, classifyPrompt string) (string, error)
	Extract(ctx context.Context, transcript string, extractionPrompt string) (string, error)
}

// NewExtractor creates the appropriate Extractor implementation based on the
// configured LLM provider. Returns an error for unsupported providers.
func NewExtractor(cfg *config.LLMConfig) (Extractor, error) {
	switch cfg.Provider {
	case "anthropic":
		return NewAnthropicExtractor(cfg.AnthropicAPIKey, cfg.Model, time.Duration(cfg.Timeout)*time.Second), nil
	case "openai-compatible":
		return NewOpenAIExtractor(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey, cfg.Model, time.Duration(cfg.Timeout)*time.Second), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", cfg.Provider)
	}
}
