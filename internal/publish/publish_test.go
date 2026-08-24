package publish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jgavinray/omniscient/internal/confluence"
	"github.com/jgavinray/omniscient/internal/models"
)

// testResult builds a deterministic ExtractionResult for sink tests.
func testResult() *models.ExtractionResult {
	return &models.ExtractionResult{
		FrontMatter: map[string]any{
			"date":         "2026-02-21",
			"meeting_type": "engineering",
			"participants": []any{"Alice", "Bob"},
		},
		Markdown: "## Summary\nQuick standup covering progress on the pipeline.\n",
	}
}

func TestMarshalResults_Empty(t *testing.T) {
	got, err := MarshalResults(nil)
	if err != nil {
		t.Fatalf("MarshalResults(nil): %v", err)
	}
	if got != "[]" {
		t.Errorf("MarshalResults(nil) = %q, want %q", got, "[]")
	}
}

func TestMarshalResults_RoundTrip(t *testing.T) {
	in := []Result{
		{Sink: "confluence", Ref: "https://wiki.example.com/pages/1"},
		{Sink: "local", Ref: "/tmp/notes.md"},
	}
	got, err := MarshalResults(in)
	if err != nil {
		t.Fatalf("MarshalResults: %v", err)
	}
	var out []Result
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("unmarshal round-trip: %v (got %q)", err, got)
	}
	if len(out) != 2 || out[0].Sink != "confluence" || out[0].Ref != "https://wiki.example.com/pages/1" ||
		out[1].Sink != "local" || out[1].Ref != "/tmp/notes.md" {
		t.Errorf("round-trip = %+v, want %+v", out, in)
	}
}

func TestFrontMatterDate(t *testing.T) {
	if got := frontMatterDate(map[string]any{"date": "2026-02-21"}); got != "2026-02-21" {
		t.Errorf("frontMatterDate = %q, want %q", got, "2026-02-21")
	}
	// Missing or non-string date falls back to today's date.
	for _, fm := range []map[string]any{nil, {}, {"date": 42}, {"date": ""}} {
		got := frontMatterDate(fm)
		if got == "" || len(got) != 10 {
			t.Errorf("frontMatterDate(%v) = %q, want a YYYY-MM-DD fallback date", fm, got)
		}
	}
}

func TestStripExt(t *testing.T) {
	cases := map[string]string{
		"Standup.gdoc": "Standup",
		"Report.docx":  "Report",
		"Note.doc":     "Note",
		"file.txt":     "file",
		"deck.pdf":     "deck",
		"noext":        "noext",
	}
	for in, want := range cases {
		if got := stripExt(in); got != want {
			t.Errorf("stripExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Team Standup 2026-02-21": "team-standup-2026-02-21",
		"  A -- B  ":              "a-b",
		"":                        "",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	// Long names are capped at 40 characters.
	long := strings.Repeat("abcdefghij", 6) // 60 chars
	if got := slugify(long); len(got) != 40 {
		t.Errorf("slugify(long) = %d chars, want 40: %q", len(got), got)
	}
}

func TestLocalSink_Publish(t *testing.T) {
	dir := t.TempDir()
	sink := NewLocalSink(dir)

	ref, err := sink.Publish(context.Background(), testResult(), "Team Standup.gdoc")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// TempDir paths are absolute, so ref must be exactly <dir>/<date>_<slug>.md.
	want := filepath.Join(dir, "2026-02-21_team-standup.md")
	if ref != want {
		t.Errorf("ref = %q, want %q", ref, want)
	}

	data, err := os.ReadFile(ref)
	if err != nil {
		t.Fatalf("read published file: %v", err)
	}
	if !strings.HasPrefix(string(data), "---\n") {
		t.Error("file does not start with YAML front-matter delimiter")
	}
	if !strings.Contains(string(data), "## Summary") {
		t.Error("file missing markdown body")
	}
	if !strings.Contains(string(data), "2026-02-21") {
		t.Error("file missing front-matter date")
	}

	// No temp file may be left behind.
	if _, err := os.Stat(ref + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file %s left behind", ref+".tmp")
	}

	// Re-publishing must overwrite without error (idempotent).
	if _, err := sink.Publish(context.Background(), testResult(), "Team Standup.gdoc"); err != nil {
		t.Fatalf("re-Publish: %v", err)
	}
}

func TestLocalSink_EmptyNameFallback(t *testing.T) {
	dir := t.TempDir()
	sink := NewLocalSink(dir)

	ref, err := sink.Publish(context.Background(), testResult(), ".gdoc")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// A name that is only an extension leaves nothing after stripExt →
	// slug falls back to "transcript".
	if want := filepath.Join(dir, "2026-02-21_transcript.md"); ref != want {
		t.Errorf("ref = %q, want %q", ref, want)
	}
}

// mockConfluenceServer emulates the Confluence REST endpoints used by
// confluence.Client: findPage (GET, empty results) and createPage (POST).
func mockConfluenceServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(map[string]interface{}{"results": []interface{}{}})
		case http.MethodPost:
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      "12345",
				"version": map[string]int{"number": 1},
				"_links":  map[string]string{"webui": "/wiki/spaces/ENG/pages/12345"},
			})
		}
	}))
}

func TestConfluenceSink_Publish(t *testing.T) {
	srv := mockConfluenceServer(t)
	defer srv.Close()

	client := confluence.NewClient(srv.URL, "test@example.com", "test-token")
	sink := NewConfluenceSink(client, "ENG", "")

	if sink.Name() != "confluence" {
		t.Errorf("Name() = %q, want %q", sink.Name(), "confluence")
	}

	// The mock returns a *relative* webui link; the client must prefix the
	// base URL (regression test for the double-protocol URL bug).
	ref, err := sink.Publish(context.Background(), testResult(), "Team Standup.gdoc")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if want := srv.URL + "/wiki/spaces/ENG/pages/12345"; ref != want {
		t.Errorf("ref = %q, want %q", ref, want)
	}
}

func TestSlackSink_Publish_OK(t *testing.T) {
	var calls atomic.Int32
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		lastBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	sink := NewSlackSink(srv.URL)
	if sink.Name() != "slack" {
		t.Errorf("Name() = %q, want %q", sink.Name(), "slack")
	}

	ref, err := sink.Publish(context.Background(), testResult(), "Team Standup.gdoc")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("webhook calls = %d, want 1", got)
	}
	if want := "slack:Team Standup - 2026-02-21"; ref != want {
		t.Errorf("ref = %q, want %q", ref, want)
	}

	// Payload sanity: title + markdown body inside the attachment.
	var payload struct {
		Text        string `json:"text"`
		Attachments []struct {
			Title    string   `json:"title"`
			Text     string   `json:"text"`
			MrkdwnIn []string `json:"mrkdwn_in"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(lastBody, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(payload.Attachments))
	}
	if payload.Attachments[0].Title != "Team Standup - 2026-02-21" {
		t.Errorf("attachment title = %q", payload.Attachments[0].Title)
	}
	if !strings.Contains(payload.Attachments[0].Text, "## Summary") {
		t.Error("attachment text missing markdown body")
	}
}

func TestSlackSink_Publish_404NoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not_found"))
	}))
	defer srv.Close()

	_, err := NewSlackSink(srv.URL).Publish(context.Background(), testResult(), "Team Standup.gdoc")
	if err == nil {
		t.Fatal("expected error for 404 webhook, got nil")
	}
	// 404 is not transient — exactly one attempt, no retry.
	if got := calls.Load(); got != 1 {
		t.Errorf("webhook calls = %d, want 1 (404 must not be retried)", got)
	}
}

func TestSlackSink_Publish_Retries500(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	// Two 500s then success: exercises the retry path (~3s of backoff).
	ref, err := NewSlackSink(srv.URL).Publish(context.Background(), testResult(), "Team Standup.gdoc")
	if err != nil {
		t.Fatalf("Publish after retries: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("webhook calls = %d, want 3", got)
	}
	if ref == "" {
		t.Error("expected non-empty ref")
	}
}
