package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const sampleMarkdownOutput = `---
date: "2026-02-21"
meeting_type: engineering
participants:
  - Alice
  - Bob
---
## Summary

Daily standup covering project progress and next steps.

## Decisions
- **Use Go for backend** (Owner: Alice) — Team expertise

## Action Items
- [ ] Write tests (Owner: Bob, Due: 2026-02-22)`

func anthropicSuccessResponse(text string) string {
	resp := map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": text},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

func TestAnthropicExtract_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, anthropicSuccessResponse(sampleMarkdownOutput))
	}))
	defer server.Close()

	e := NewAnthropicExtractor("sk-ant-test-key", "claude-sonnet-4-20250514", 10*time.Second)
	e.baseURL = server.URL

	result, err := e.Extract("Alice: Sprint looks good.\nBob: Agreed.", "Extract: {{TRANSCRIPT}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// Should contain the markdown content.
	if len(result) < 50 {
		t.Errorf("expected substantial markdown output, got %d chars", len(result))
	}
}

func TestAnthropicExtract_EmptyContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content": []}`)
	}))
	defer server.Close()

	e := NewAnthropicExtractor("sk-ant-test-key", "claude-sonnet-4-20250514", 10*time.Second)
	e.baseURL = server.URL

	_, err := e.Extract("some transcript", "Extract: {{TRANSCRIPT}}")
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
}

func TestAnthropicExtract_ServerError(t *testing.T) {
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
		fmt.Fprint(w, anthropicSuccessResponse(sampleMarkdownOutput))
	}))
	defer server.Close()

	e := NewAnthropicExtractor("sk-ant-test-key", "claude-sonnet-4-20250514", 10*time.Second)
	e.baseURL = server.URL

	result, err := e.Extract("some transcript", "Extract: {{TRANSCRIPT}}")
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

func TestAnthropicExtract_RateLimit(t *testing.T) {
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
		fmt.Fprint(w, anthropicSuccessResponse(sampleMarkdownOutput))
	}))
	defer server.Close()

	e := NewAnthropicExtractor("sk-ant-test-key", "claude-sonnet-4-20250514", 10*time.Second)
	e.baseURL = server.URL

	result, err := e.Extract("some transcript", "Extract: {{TRANSCRIPT}}")
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

func TestAnthropicExtract_PermanentError(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error": "unauthorized"}`)
	}))
	defer server.Close()

	e := NewAnthropicExtractor("sk-ant-test-key", "claude-sonnet-4-20250514", 10*time.Second)
	e.baseURL = server.URL

	_, err := e.Extract("some transcript", "Extract: {{TRANSCRIPT}}")
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}

	finalCount := callCount.Load()
	if finalCount != 1 {
		t.Errorf("expected exactly 1 call (no retry for 401), got %d", finalCount)
	}
}

func TestAnthropicClassify_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, anthropicSuccessResponse("engineering"))
	}))
	defer server.Close()

	e := NewAnthropicExtractor("sk-ant-test-key", "claude-sonnet-4-20250514", 10*time.Second)
	e.baseURL = server.URL

	result, err := e.Classify("Alice: Sprint looks good.", []string{"engineering", "planning"}, "Classify: {{TEMPLATE_KEYS}}\n{{TRANSCRIPT_PREVIEW}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "engineering" {
		t.Errorf("expected %q, got %q", "engineering", result)
	}
}

func TestAnthropicClassify_ReturnsRawKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Simulate LLM returning key with extra whitespace.
		fmt.Fprint(w, anthropicSuccessResponse("  customer_success  \n"))
	}))
	defer server.Close()

	e := NewAnthropicExtractor("sk-ant-test-key", "claude-sonnet-4-20250514", 10*time.Second)
	e.baseURL = server.URL

	result, err := e.Classify("preview text", []string{"engineering", "customer_success"}, "Classify: {{TEMPLATE_KEYS}}\n{{TRANSCRIPT_PREVIEW}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "customer_success" {
		t.Errorf("expected %q, got %q", "customer_success", result)
	}
}
