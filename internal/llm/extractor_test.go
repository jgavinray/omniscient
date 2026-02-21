package llm

import (
	"testing"

	"github.com/jgavinray/omniscient/internal/config"
)

func TestNewExtractor_Anthropic(t *testing.T) {
	cfg := &config.LLMConfig{
		Provider:        "anthropic",
		AnthropicAPIKey: "sk-ant-test-key",
		Model:           "claude-sonnet-4-20250514",
		Timeout:         30,
	}

	ext, err := NewExtractor(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := ext.(*AnthropicExtractor); !ok {
		t.Errorf("expected *AnthropicExtractor, got %T", ext)
	}
}

func TestNewExtractor_OpenAI(t *testing.T) {
	cfg := &config.LLMConfig{
		Provider:      "openai-compatible",
		OpenAIBaseURL: "http://localhost:8080",
		OpenAIAPIKey:  "test-key",
		Model:         "gpt-4",
		Timeout:       30,
	}

	ext, err := NewExtractor(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := ext.(*OpenAIExtractor); !ok {
		t.Errorf("expected *OpenAIExtractor, got %T", ext)
	}
}

func TestNewExtractor_UnsupportedProvider(t *testing.T) {
	cfg := &config.LLMConfig{
		Provider: "gemini",
		Model:    "gemini-pro",
		Timeout:  30,
	}

	_, err := NewExtractor(cfg)
	if err == nil {
		t.Fatal("expected error for unsupported provider, got nil")
	}

	expected := `unsupported LLM provider: gemini`
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}
