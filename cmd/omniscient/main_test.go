package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/confluence"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/jgavinray/omniscient/internal/drive"
	"github.com/jgavinray/omniscient/internal/llm"
	"github.com/jgavinray/omniscient/internal/models"
)

// sampleExtractionOutput is the raw YAML+markdown output the mock LLM returns.
const sampleExtractionOutput = `---
date: "2026-02-21"
meeting_type: engineering
participants:
  - Alice
  - Bob
---

## Summary
Quick standup covering progress on Omniscient project.

## Decisions
- **Use Go** (Owner: Alice) — Team expertise

## Action Items
- [ ] Write tests (Owner: Bob, Due: 2026-02-22)
`

// TestIntegration_FullPipeline tests the full sync pipeline with all external
// services mocked: LLM classification/extraction and Confluence publishing.
// Google Drive is simulated by constructing Transcript structs directly.
func TestIntegration_FullPipeline(t *testing.T) {
	ctx := context.Background()

	// --- Mock LLM server (OpenAI-compatible format) ---
	var llmCalls atomic.Int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCalls.Add(1)

		// Decode the request to determine if this is a classify or extract call.
		var reqBody struct {
			MaxTokens int `json:"max_tokens"`
		}
		json.NewDecoder(r.Body).Decode(&reqBody)

		var content string
		if reqBody.MaxTokens <= 32 {
			// Classification call — return a meeting type key.
			content = "engineering"
		} else {
			// Extraction call — return YAML+markdown output.
			content = sampleExtractionOutput
		}

		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": content}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer llmServer.Close()

	// --- Mock Confluence server ---
	var confluenceCalls atomic.Int32
	confluenceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		confluenceCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet {
			// findPage — return empty results
			json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []interface{}{},
			})
			return
		}

		if r.Method == http.MethodPost {
			// createPage
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      "12345",
				"version": map[string]int{"number": 1},
				"_links":  map[string]string{"webui": "/wiki/spaces/ENG/pages/12345"},
			})
			return
		}
	}))
	defer confluenceServer.Close()

	// --- Set up SQLite database in temp dir ---
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := database.NewStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	// --- Create LLM extractor pointing at mock server ---
	llmCfg := &config.LLMConfig{
		Provider:      "openai-compatible",
		OpenAIBaseURL: llmServer.URL,
		OpenAIAPIKey:  "test-key",
		Model:         "test-model",
		Timeout:       30,
	}
	extractor, err := llm.NewExtractor(llmCfg)
	if err != nil {
		t.Fatalf("create extractor: %v", err)
	}

	// --- Create Confluence client pointing at mock server ---
	confClient := confluence.NewClient(confluenceServer.URL, "test@example.com", "test-token")

	// --- Simulate transcripts from Drive ---
	transcripts := []*drive.Transcript{
		{ID: "doc-001", Name: "Team Standup.gdoc", Content: "Alice: I worked on the pipeline.\nBob: I'll write tests."},
		{ID: "doc-002", Name: "Design Review.gdoc", Content: "Alice: Let's review the architecture.\nBob: Looks good."},
	}

	// --- Set up prompts config for template lookup ---
	classifyPrompt := "Classify this meeting: {{TEMPLATE_KEYS}}\n{{TRANSCRIPT_PREVIEW}}"
	templates := map[string]config.MeetingTemplate{
		"engineering": {
			Description:      "Engineering meetings",
			ExtractionPrompt: "Extract notes from this engineering transcript:\n{{TRANSCRIPT}}",
		},
	}
	templateKeys := []string{"engineering"}

	// --- Run the pipeline ---
	spaceKey := "ENG"
	parentPageID := ""
	successCount := 0

	for _, transcript := range transcripts {
		processed, err := store.IsProcessed(ctx, transcript.ID)
		if err != nil {
			t.Errorf("check processed: %v", err)
			continue
		}
		if processed {
			continue
		}

		// Classify.
		preview := transcript.Content
		if len(preview) > 1000 {
			preview = preview[:1000]
		}
		meetingType, err := extractor.Classify(ctx, preview, templateKeys, classifyPrompt)
		if err != nil {
			t.Errorf("classify failed for %s: %v", transcript.ID, err)
			continue
		}

		tmpl, ok := templates[strings.TrimSpace(meetingType)]
		if !ok {
			tmpl = templates["engineering"]
		}

		// Extract.
		rawOutput, err := extractor.Extract(ctx, transcript.Content, tmpl.ExtractionPrompt)
		if err != nil {
			t.Errorf("extraction failed for %s: %v", transcript.ID, err)
			continue
		}

		// Parse.
		result, err := models.ParseExtractionOutput(rawOutput)
		if err != nil {
			t.Errorf("parse failed for %s: %v", transcript.ID, err)
			continue
		}

		// Publish.
		confluenceURL, err := confClient.PublishMarkdown(ctx, spaceKey, parentPageID, result, transcript.Name)
		if err != nil {
			t.Errorf("publish failed for %s: %v", transcript.ID, err)
			continue
		}

		if err := store.MarkProcessed(ctx, transcript.ID, transcript.Name, confluenceURL); err != nil {
			t.Errorf("mark processed failed for %s: %v", transcript.ID, err)
		}

		successCount++
	}

	// --- Verify results ---
	if successCount != 2 {
		t.Errorf("expected 2 successes, got %d", successCount)
	}

	// 2 classify + 2 extract = 4 LLM calls
	if llmCalls.Load() != 4 {
		t.Errorf("expected 4 LLM calls (2 classify + 2 extract), got %d", llmCalls.Load())
	}

	// 2 findPage (GET) + 2 createPage (POST) = 4 confluence calls
	if confluenceCalls.Load() != 4 {
		t.Errorf("expected 4 confluence calls, got %d", confluenceCalls.Load())
	}

	// Verify both are now marked as processed.
	for _, transcript := range transcripts {
		processed, err := store.IsProcessed(ctx, transcript.ID)
		if err != nil {
			t.Errorf("check processed after: %v", err)
		}
		if !processed {
			t.Errorf("transcript %s should be processed", transcript.ID)
		}
	}

	// --- Run pipeline again — should skip both ---
	secondRunSuccess := 0
	for _, transcript := range transcripts {
		processed, _ := store.IsProcessed(ctx, transcript.ID)
		if !processed {
			secondRunSuccess++
		}
	}

	if secondRunSuccess != 0 {
		t.Errorf("expected 0 pending on second run, got %d", secondRunSuccess)
	}
}

func TestConfigValidate_ExampleConfig(t *testing.T) {
	// Verify that config.yaml.example loads and validates successfully.
	examplePath := filepath.Join("..", "..", "config.yaml.example")
	if _, err := os.Stat(examplePath); os.IsNotExist(err) {
		t.Skip("config.yaml.example not found at expected path")
	}

	cfg, err := config.Load(examplePath)
	if err != nil {
		t.Fatalf("example config should be valid: %v", err)
	}

	if cfg.LLM.Provider != "openai-compatible" {
		t.Errorf("expected provider openai-compatible, got %s", cfg.LLM.Provider)
	}
}

// --- CLI command tests ---

// tempYAML writes a minimal valid config YAML with the given database_path
// and returns the path to the file.
func tempYAML(t *testing.T, dbPath string) string {
	t.Helper()
	dir := t.TempDir()
	content := fmt.Sprintf(`google:
  credentials_file: /opt/omniscient/credentials.json
  token_file: /opt/omniscient/token.json
  folder_id: abc123folderID
llm:
  provider: openai-compatible
  openai_base_url: http://localhost:11434/v1
  openai_api_key: test-key-123
  model: llama3:70b
  timeout: 90
confluence:
  base_url: https://mycompany.atlassian.net/wiki
  email: user@example.com
  api_token: confluence-token-xyz
  space_key: ENG
  parent_page_id: "12345"
sync:
  lookback_hours: 48
  database_path: %s
  max_per_run: 25
logging:
  level: debug
prompts:
  classify_prompt: "Classify this meeting: {{TEMPLATE_KEYS}}\n{{TRANSCRIPT_PREVIEW}}"
  templates:
    engineering:
      description: "Engineering meetings"
      extraction_prompt: "Extract notes from this engineering transcript:\n{{TRANSCRIPT}}"
`, dbPath)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}
	return path
}

func setTestConfig(t *testing.T, path string) {
	t.Helper()
	old := cfgFile
	cfgFile = path
	t.Cleanup(func() { cfgFile = old })
}

func TestStatusCmd_EmptyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	configPath := tempYAML(t, dbPath)
	setTestConfig(t, configPath)

	cmd := newStatusCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("status command failed: %v", err)
	}

	output := buf.String()
	// All five statuses should appear with count 0.
	for _, status := range []string{
		database.StatusDiscovered,
		database.StatusExtracted,
		database.StatusFailed,
		database.StatusPublished,
		database.StatusSkipped,
	} {
		if !strings.Contains(output, status) {
			t.Errorf("output missing status %q", status)
		}
		// Each status line should have a 0 count.
		if !strings.Contains(output, fmt.Sprintf("%s\t0\n", status)) {
			t.Errorf("expected zero count line for %q, got:\n%s", status, output)
		}
	}
}

func TestStatusCmd_WithRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	configPath := tempYAML(t, dbPath)
	setTestConfig(t, configPath)

	store, err := database.NewStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Insert a discovered transcript.
	if err := store.MarkDiscovered(ctx, "doc-001", "Meeting 1"); err != nil {
		t.Fatalf("MarkDiscovered: %v", err)
	}

	// Insert a failed transcript.
	if err := store.MarkFailed(ctx, "doc-002", "Meeting 2", "extraction error"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	// Insert a published transcript.
	if err := store.MarkProcessed(ctx, "doc-003", "Meeting 3", "https://confluence.example.com/page"); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	cmd := newStatusCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("status command failed: %v", err)
	}

	output := buf.String()

	// Verify counts: 1 discovered, 1 failed, 1 published, 0 extracted, 0 skipped.
	if !strings.Contains(output, "discovered\t1\n") {
		t.Errorf("expected discovered count 1, got:\n%s", output)
	}
	if !strings.Contains(output, "failed\t1\n") {
		t.Errorf("expected failed count 1, got:\n%s", output)
	}
	if !strings.Contains(output, "published\t1\n") {
		t.Errorf("expected published count 1, got:\n%s", output)
	}
	if !strings.Contains(output, "extracted\t0\n") {
		t.Errorf("expected extracted count 0, got:\n%s", output)
	}
	if !strings.Contains(output, "skipped\t0\n") {
		t.Errorf("expected skipped count 0, got:\n%s", output)
	}

	// Verify recent rows appear.
	if !strings.Contains(output, "doc-001") || !strings.Contains(output, "doc-002") || !strings.Contains(output, "doc-003") {
		t.Errorf("expected recent rows for all three transcripts, got:\n%s", output)
	}
}

func TestRetryFailedCmd(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	configPath := tempYAML(t, dbPath)
	setTestConfig(t, configPath)

	store, err := database.NewStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Insert a failed transcript.
	if err := store.MarkFailed(ctx, "doc-001", "Meeting 1", "some error"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	// Insert a published transcript (should NOT be affected).
	if err := store.MarkProcessed(ctx, "doc-002", "Meeting 2", "https://confluence.example.com/page"); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	cmd := newRetryFailedCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("retry-failed command failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "retry-failed\t1\n") {
		t.Errorf("expected 'retry-failed\t1', got:\n%s", output)
	}

	// Verify failed → discovered.
	rec, err := store.GetTranscript(ctx, "doc-001")
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if rec.Status != database.StatusDiscovered {
		t.Errorf("expected doc-001 status %q, got %q", database.StatusDiscovered, rec.Status)
	}

	// Verify published stays published.
	rec, err = store.GetTranscript(ctx, "doc-002")
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if rec.Status != database.StatusPublished {
		t.Errorf("expected doc-002 status %q, got %q", database.StatusPublished, rec.Status)
	}
}

func TestForgetCmd(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	configPath := tempYAML(t, dbPath)
	setTestConfig(t, configPath)

	store, err := database.NewStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Insert two transcripts.
	if err := store.MarkDiscovered(ctx, "doc-001", "Meeting 1"); err != nil {
		t.Fatalf("MarkDiscovered: %v", err)
	}
	if err := store.MarkDiscovered(ctx, "doc-002", "Meeting 2"); err != nil {
		t.Fatalf("MarkDiscovered: %v", err)
	}

	cmd := newForgetCmd()
	cmd.SetArgs([]string{"doc-001"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("forget command failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "forget\tdoc-001\t1\n") {
		t.Errorf("expected 'forget\tdoc-001\t1', got:\n%s", output)
	}

	// Verify doc-001 is deleted.
	rec, err := store.GetTranscript(ctx, "doc-001")
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if rec != nil {
		t.Errorf("expected doc-001 to be deleted, got %+v", rec)
	}

	// Verify doc-002 is still present.
	rec, err = store.GetTranscript(ctx, "doc-002")
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if rec == nil {
		t.Error("expected doc-002 to still exist")
	}
}
