package confluence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jgavinray/omniscient/internal/models"
	"github.com/jgavinray/omniscient/internal/retry"
)

// sampleExtractionResult returns a fully populated ExtractionResult for use in tests.
func sampleExtractionResult() *models.ExtractionResult {
	return &models.ExtractionResult{
		FrontMatter: map[string]any{
			"date":         "2026-01-15",
			"meeting_type": "Sprint Planning",
			"participants": []any{"Alice", "Bob", "Charlie"},
		},
		Markdown: "## Summary\nTeam discussed sprint goals and migration plan.\n\n## Decisions\n- Migrate to Go 1.23\n\n## Blockers\n- CI pipeline timeout\n\n## Action Items\n- Update CI config (Bob, due 2026-01-20)",
	}
}

// successPageResponse returns a JSON-encoded page result for mock responses.
func successPageResponse(id string, version int) []byte {
	result := pageResult{
		ID: id,
	}
	result.Version.Number = version
	result.Links.WebUI = "/wiki/spaces/ENG/pages/" + id

	data, _ := json.Marshal(result)
	return data
}

func withImmediateConfluenceRetries(t *testing.T) {
	t.Helper()
	old := retryDoContext
	retryDoContext = func(ctx context.Context, fn func() error, maxAttempts int) error {
		var lastErr error
		for attempt := 0; attempt < maxAttempts; attempt++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = fn()
			if lastErr == nil {
				return nil
			}
			if !retry.IsTransient(lastErr) {
				return lastErr
			}
		}
		return fmt.Errorf("all %d attempts failed, last error: %w", maxAttempts, lastErr)
	}
	t.Cleanup(func() {
		retryDoContext = old
	})
}

func TestPublishMarkdown_URLNormalization(t *testing.T) {
	ctx := context.Background()

	var capturedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write(successPageResponse("456", 1))
	}))
	defer server.Close()

	// Pass a base URL with a trailing "/wiki" segment.
	client := NewClient(server.URL+"/wiki", "test@example.com", "test-token")
	result := sampleExtractionResult()

	pageURL, err := client.PublishMarkdown(ctx, "ENG", "100", result, "URL Norm Test.gdoc")
	if err != nil {
		t.Fatalf("PublishMarkdown returned unexpected error: %v", err)
	}

	// The client strips the trailing "/wiki" from the base URL, so the
	// create request path should be "/wiki/rest/api/content" (not "/wiki/wiki/rest/api/content").
	if capturedPath != "/wiki/rest/api/content" {
		t.Errorf("expected path /wiki/rest/api/content, got %s", capturedPath)
	}

	// The returned pageURL must be server.URL + "/wiki/spaces/ENG/pages/456" exactly once.
	expectedURL := server.URL + "/wiki/spaces/ENG/pages/456"
	if pageURL != expectedURL {
		t.Errorf("expected URL %s, got %s", expectedURL, pageURL)
	}
}

func TestPublishMarkdown_CreateNewPage(t *testing.T) {
	ctx := context.Background()
	var (
		capturedMethod string
		capturedPath   string
		capturedBody   map[string]interface{}
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/wiki/rest/api/content"):
			// findPage: return empty results so a new page is created.
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"results": []}`))

		case r.Method == http.MethodPost && r.URL.Path == "/wiki/rest/api/content":
			// createPage: capture the request for assertions.
			capturedMethod = r.Method
			capturedPath = r.URL.Path

			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			capturedBody = body

			w.Header().Set("Content-Type", "application/json")
			w.Write(successPageResponse("456", 1))

		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test@example.com", "test-token")
	result := sampleExtractionResult()

	pageURL, err := client.PublishMarkdown(ctx, "ENG", "100", result, "Sprint Planning Notes.gdoc")
	if err != nil {
		t.Fatalf("PublishMarkdown returned unexpected error: %v", err)
	}

	// Verify the create request was made.
	if capturedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", capturedMethod)
	}
	if capturedPath != "/wiki/rest/api/content" {
		t.Errorf("expected path /wiki/rest/api/content, got %s", capturedPath)
	}

	// Verify space key.
	spaceMap, ok := capturedBody["space"].(map[string]interface{})
	if !ok {
		t.Fatal("expected space key in request body")
	}
	if spaceMap["key"] != "ENG" {
		t.Errorf("expected space key ENG, got %v", spaceMap["key"])
	}

	// Verify title format (extension stripped, date appended).
	if capturedBody["title"] != "Sprint Planning Notes - 2026-01-15" {
		t.Errorf("expected title 'Sprint Planning Notes - 2026-01-15', got %v", capturedBody["title"])
	}

	// Verify HTML body is present in the storage format.
	bodyMap, ok := capturedBody["body"].(map[string]interface{})
	if !ok {
		t.Fatal("expected body in request")
	}
	storageMap, ok := bodyMap["storage"].(map[string]interface{})
	if !ok {
		t.Fatal("expected storage in body")
	}
	htmlBody, ok := storageMap["value"].(string)
	if !ok || htmlBody == "" {
		t.Fatal("expected non-empty HTML body")
	}
	if !strings.Contains(htmlBody, "<h2>Summary</h2>") {
		t.Error("HTML body should contain Summary section")
	}

	// Verify returned URL.
	expectedURL := server.URL + "/wiki/spaces/ENG/pages/456"
	if pageURL != expectedURL {
		t.Errorf("expected URL %s, got %s", expectedURL, pageURL)
	}
}

func TestPublishMarkdown_UpdateExistingPage(t *testing.T) {
	ctx := context.Background()
	var capturedUpdateVersion int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/wiki/rest/api/content"):
			// findPage: return an existing page with version 3.
			resp := searchResponse{
				Results: []pageResult{
					{
						ID: "789",
					},
				},
			}
			resp.Results[0].Version.Number = 3
			resp.Results[0].Links.WebUI = "/wiki/spaces/ENG/pages/789"

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/wiki/rest/api/content/789"):
			// updatePage: capture the version from the request body.
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)

			versionMap, ok := body["version"].(map[string]interface{})
			if ok {
				if num, ok := versionMap["number"].(float64); ok {
					capturedUpdateVersion = int(num)
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.Write(successPageResponse("789", capturedUpdateVersion))

		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test@example.com", "test-token")
	result := sampleExtractionResult()

	_, err := client.PublishMarkdown(ctx, "ENG", "", result, "Standup Notes")
	if err != nil {
		t.Fatalf("PublishMarkdown returned unexpected error: %v", err)
	}

	// Verify version was incremented from 3 to 4.
	if capturedUpdateVersion != 4 {
		t.Errorf("expected version 4 (3+1), got %d", capturedUpdateVersion)
	}
}

func TestPublishMarkdown_StripExtension(t *testing.T) {
	ctx := context.Background()
	var capturedTitle string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/wiki/rest/api/content"):
			capturedTitle = r.URL.Query().Get("title")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"results": []}`))

		case r.Method == http.MethodPost && r.URL.Path == "/wiki/rest/api/content":
			w.Header().Set("Content-Type", "application/json")
			w.Write(successPageResponse("101", 1))

		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test@example.com", "test-token")
	result := sampleExtractionResult()

	_, err := client.PublishMarkdown(ctx, "ENG", "", result, "Team Standup.gdoc")
	if err != nil {
		t.Fatalf("PublishMarkdown returned unexpected error: %v", err)
	}

	expected := "Team Standup - 2026-01-15"
	if capturedTitle != expected {
		t.Errorf("expected title %q, got %q", expected, capturedTitle)
	}
}

func TestMarkdownToHTML(t *testing.T) {
	md := "## Summary\nTeam discussed goals.\n\n- Item one\n- Item two\n"

	html, err := markdownToHTML(md)
	if err != nil {
		t.Fatalf("markdownToHTML error: %v", err)
	}

	if !strings.Contains(html, "<h2>Summary</h2>") {
		t.Error("expected <h2>Summary</h2> in output")
	}
	if !strings.Contains(html, "<li>Item one</li>") {
		t.Error("expected list items in output")
	}
}

func TestMarkdownToHTML_Table(t *testing.T) {
	md := "| Name | Role |\n| --- | --- |\n| Alice | Lead |\n"

	html, err := markdownToHTML(md)
	if err != nil {
		t.Fatalf("markdownToHTML error: %v", err)
	}

	if !strings.Contains(html, "<table>") {
		t.Error("expected <table> in output (GFM tables should be supported)")
	}
	if !strings.Contains(html, "Alice") {
		t.Error("expected table content in output")
	}
}

func TestExtractDate_FromFrontMatter(t *testing.T) {
	fm := map[string]any{"date": "2026-03-15"}
	got := extractDate(fm)
	if got != "2026-03-15" {
		t.Errorf("expected 2026-03-15, got %s", got)
	}
}

func TestExtractDate_MissingFallsBackToToday(t *testing.T) {
	fm := map[string]any{"meeting_type": "standup"}
	got := extractDate(fm)
	// Should be a valid date string (today), not empty.
	if got == "" {
		t.Error("expected non-empty date fallback")
	}
}

func TestPublishMarkdown_ServerError_Retry(t *testing.T) {
	withImmediateConfluenceRetries(t)
	ctx := context.Background()
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/wiki/rest/api/content"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"results": []}`))

		case r.Method == http.MethodPost && r.URL.Path == "/wiki/rest/api/content":
			count := requestCount.Add(1)
			if count <= 2 {
				// First two attempts return 500.
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"message": "internal server error"}`))
				return
			}
			// Third attempt succeeds.
			w.Header().Set("Content-Type", "application/json")
			w.Write(successPageResponse("999", 1))

		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test@example.com", "test-token")
	client.httpClient.Timeout = 0

	result := sampleExtractionResult()

	pageURL, err := client.PublishMarkdown(ctx, "ENG", "", result, "Retry Test")
	if err != nil {
		t.Fatalf("PublishMarkdown should succeed after retries, got error: %v", err)
	}

	finalCount := requestCount.Load()
	if finalCount != 3 {
		t.Errorf("expected 3 POST requests (2 failures + 1 success), got %d", finalCount)
	}

	expectedURL := server.URL + "/wiki/spaces/ENG/pages/999"
	if pageURL != expectedURL {
		t.Errorf("expected URL %s, got %s", expectedURL, pageURL)
	}
}

func TestPublishMarkdown_RateLimit_Retry(t *testing.T) {
	withImmediateConfluenceRetries(t)
	ctx := context.Background()
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/wiki/rest/api/content"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"results": []}`))

		case r.Method == http.MethodPost && r.URL.Path == "/wiki/rest/api/content":
			count := requestCount.Add(1)
			if count <= 1 {
				// First attempt returns 429.
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"message": "rate limited"}`))
				return
			}
			// Second attempt succeeds.
			w.Header().Set("Content-Type", "application/json")
			w.Write(successPageResponse("888", 1))

		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test@example.com", "test-token")
	client.httpClient.Timeout = 0

	result := sampleExtractionResult()

	pageURL, err := client.PublishMarkdown(ctx, "ENG", "", result, "Rate Limit Test")
	if err != nil {
		t.Fatalf("PublishMarkdown should succeed after rate limit retry, got error: %v", err)
	}

	finalCount := requestCount.Load()
	if finalCount != 2 {
		t.Errorf("expected 2 POST requests (1 rate limit + 1 success), got %d", finalCount)
	}

	expectedURL := server.URL + "/wiki/spaces/ENG/pages/888"
	if pageURL != expectedURL {
		t.Errorf("expected URL %s, got %s", expectedURL, pageURL)
	}
}

func TestPublisherImplementsDestination(t *testing.T) {
	p := NewPublisher("https://example.atlassian.net", "a@b.c", "tok", "ENG", "")
	if got := p.Name(); got != "confluence" {
		t.Errorf("Name() = %q, want %q", got, "confluence")
	}
}
