package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func openaiSuccessResponse(text string) string {
	resp := map[string]interface{}{
		"choices": []map[string]interface{}{
			{
				"message": map[string]interface{}{
					"content": text,
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

func TestOpenAIExtract_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, openaiSuccessResponse(sampleMarkdownOutput))
	}))
	defer server.Close()

	e := NewOpenAIExtractor(server.URL, "test-key", "test-model", 10*time.Second)

	result, err := e.Extract(context.Background(), "Alice: Sprint looks good.\nBob: Agreed.", "Extract: {{TRANSCRIPT}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if len(result) < 50 {
		t.Errorf("expected substantial markdown output, got %d chars", len(result))
	}
}

func TestOpenAIExtract_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": []}`)
	}))
	defer server.Close()

	e := NewOpenAIExtractor(server.URL, "test-key", "test-model", 10*time.Second)

	_, err := e.Extract(context.Background(), "some transcript", "Extract: {{TRANSCRIPT}}")
	if err == nil {
		t.Fatal("expected error for empty choices, got nil")
	}
}

func TestOpenAIExtract_ServerError(t *testing.T) {
	withImmediateRetries(t)
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		if count <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error": "internal server error"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, openaiSuccessResponse(sampleMarkdownOutput))
	}))
	defer server.Close()

	e := NewOpenAIExtractor(server.URL, "test-key", "test-model", 10*time.Second)

	result, err := e.Extract(context.Background(), "some transcript", "Extract: {{TRANSCRIPT}}")
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty result after retries")
	}

	finalCount := callCount.Load()
	if finalCount != 3 {
		t.Errorf("expected 3 total calls (2 failures + 1 success), got %d", finalCount)
	}
}

func TestOpenAIExtract_RateLimit(t *testing.T) {
	withImmediateRetries(t)
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		if count == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error": "rate limited"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, openaiSuccessResponse(sampleMarkdownOutput))
	}))
	defer server.Close()

	e := NewOpenAIExtractor(server.URL, "test-key", "test-model", 10*time.Second)

	result, err := e.Extract(context.Background(), "some transcript", "Extract: {{TRANSCRIPT}}")
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty result")
	}

	finalCount := callCount.Load()
	if finalCount != 2 {
		t.Errorf("expected 2 total calls (1 rate limit + 1 success), got %d", finalCount)
	}
}

func TestOpenAIExtract_PermanentError(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error": "unauthorized"}`)
	}))
	defer server.Close()

	e := NewOpenAIExtractor(server.URL, "test-key", "test-model", 10*time.Second)

	_, err := e.Extract(context.Background(), "some transcript", "Extract: {{TRANSCRIPT}}")
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}

	finalCount := callCount.Load()
	if finalCount != 1 {
		t.Errorf("expected exactly 1 call (no retry for 401), got %d", finalCount)
	}
}

func TestOpenAIClassify_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, openaiSuccessResponse("planning"))
	}))
	defer server.Close()

	e := NewOpenAIExtractor(server.URL, "test-key", "test-model", 10*time.Second)

	result, err := e.Classify(context.Background(), "Alice: Let's plan the sprint.", []string{"engineering", "planning"}, "Classify: {{TEMPLATE_KEYS}}\n{{TRANSCRIPT_PREVIEW}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "planning" {
		t.Errorf("expected %q, got %q", "planning", result)
	}
}

func TestOpenAIClassify_ReturnsRawKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, openaiSuccessResponse("  engineering  \n"))
	}))
	defer server.Close()

	e := NewOpenAIExtractor(server.URL, "test-key", "test-model", 10*time.Second)

	result, err := e.Classify(context.Background(), "preview text", []string{"engineering", "customer_success"}, "Classify: {{TEMPLATE_KEYS}}\n{{TRANSCRIPT_PREVIEW}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "engineering" {
		t.Errorf("expected %q, got %q", "engineering", result)
	}
}
