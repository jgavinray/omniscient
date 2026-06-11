package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/jgavinray/omniscient/internal/models"
)

// --- fakes -----------------------------------------------------------------

type fakeSource struct {
	name        string
	transcripts []*models.Transcript
	err         error
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) ListRecent(ctx context.Context, since time.Duration) ([]*models.Transcript, error) {
	return f.transcripts, f.err
}

type fakeExtractor struct {
	classifyResults []string // popped in order; repeats last
	classifyCalls   []string // prompts received
	extractResults  []string // popped in order; repeats last
	extractCalls    []string // prompts received
}

func (f *fakeExtractor) Classify(ctx context.Context, preview string, keys []string, prompt string) (string, error) {
	f.classifyCalls = append(f.classifyCalls, prompt)
	return f.pop(&f.classifyResults), nil
}

func (f *fakeExtractor) Extract(ctx context.Context, transcript string, prompt string) (string, error) {
	f.extractCalls = append(f.extractCalls, prompt)
	return f.pop(&f.extractResults), nil
}

func (f *fakeExtractor) pop(s *[]string) string {
	if len(*s) == 0 {
		return ""
	}
	v := (*s)[0]
	if len(*s) > 1 {
		*s = (*s)[1:]
	}
	return v
}

type fakeDestination struct {
	name      string
	url       string
	err       error
	published []*models.Transcript
}

func (f *fakeDestination) Name() string { return f.name }
func (f *fakeDestination) Publish(ctx context.Context, r *models.ExtractionResult, t *models.Transcript) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.published = append(f.published, t)
	return f.url, nil
}

type fakeStore struct {
	processed map[string]string // key -> urls JSON
	failed    map[string]string // key -> error message
}

func newFakeStore() *fakeStore {
	return &fakeStore{processed: map[string]string{}, failed: map[string]string{}}
}

func (f *fakeStore) IsProcessed(ctx context.Context, key string) (bool, error) {
	_, ok := f.processed[key]
	return ok, nil
}

func (f *fakeStore) MarkProcessed(ctx context.Context, key, name, urls string) error {
	f.processed[key] = urls
	return nil
}

func (f *fakeStore) MarkFailed(ctx context.Context, key, name, msg string) error {
	f.failed[key] = msg
	return nil
}

func (f *fakeStore) RecordSyncEvent(ctx context.Context, e *database.SyncEvent) error { return nil }

// --- helpers ---------------------------------------------------------------

const validOutput = "---\ndate: \"2026-06-11\"\n---\n## Summary\nNotes."

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Sync.MaxPerRun = 50
	cfg.Sync.LookbackHours = 24
	cfg.LLM.MaxTranscriptChars = 100000
	cfg.Prompts.ClassifyPrompt = "{{TEMPLATE_KEYS}} {{TRANSCRIPT_PREVIEW}}"
	cfg.Prompts.Templates = map[string]config.MeetingTemplate{
		"engineering": {ExtractionPrompt: "extract {{TRANSCRIPT}}"},
	}
	return cfg
}

func testTranscript(id string) *models.Transcript {
	return &models.Transcript{ID: id, Source: "fake", Title: "Meeting " + id, Content: "hello world"}
}

// --- tests -----------------------------------------------------------------

func TestRunPublishesToAllDestinations(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	ext := &fakeExtractor{classifyResults: []string{"engineering"}, extractResults: []string{validOutput}}
	d1 := &fakeDestination{name: "confluence", url: "https://c/1"}
	d2 := &fakeDestination{name: "notion", url: "https://n/1"}
	store := newFakeStore()

	svc := New([]Source{src}, ext, []Destination{d1, d2}, store, testConfig())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	urls, ok := store.processed["fake:t1"]
	if !ok {
		t.Fatal("transcript not marked processed under source-prefixed key")
	}
	if !strings.Contains(urls, "https://c/1") || !strings.Contains(urls, "https://n/1") {
		t.Errorf("published URLs JSON missing destinations: %s", urls)
	}
	if len(d1.published) != 1 || len(d2.published) != 1 {
		t.Errorf("publish counts: confluence=%d notion=%d, want 1 and 1", len(d1.published), len(d2.published))
	}
}

func TestRunPartialPublishFailureMarksFailed(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	ext := &fakeExtractor{classifyResults: []string{"engineering"}, extractResults: []string{validOutput}}
	good := &fakeDestination{name: "confluence", url: "https://c/1"}
	bad := &fakeDestination{name: "notion", err: errors.New("boom")}
	store := newFakeStore()

	svc := New([]Source{src}, ext, []Destination{good, bad}, store, testConfig())
	_ = svc.Run(context.Background())

	if _, ok := store.processed["fake:t1"]; ok {
		t.Error("transcript must NOT be marked processed when any destination fails")
	}
	if _, ok := store.failed["fake:t1"]; !ok {
		t.Error("transcript must be marked failed on partial publish failure")
	}
}

func TestClassifyInvalidRetriesThenFallsBack(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	// Both attempts return garbage -> pipeline falls back to the only template.
	ext := &fakeExtractor{classifyResults: []string{"banana", "still-banana"}, extractResults: []string{validOutput}}
	d := &fakeDestination{name: "confluence", url: "https://c/1"}
	store := newFakeStore()

	svc := New([]Source{src}, ext, []Destination{d}, store, testConfig())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(ext.classifyCalls) != 2 {
		t.Errorf("classify calls = %d, want 2 (initial + corrective retry)", len(ext.classifyCalls))
	}
	if !strings.Contains(ext.classifyCalls[1], "banana") {
		t.Errorf("retry prompt should quote the bad answer, got: %s", ext.classifyCalls[1])
	}
	if _, ok := store.processed["fake:t1"]; !ok {
		t.Error("fallback template should still allow processing to complete")
	}
}

func TestExtractMalformedRetriesWithFeedback(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	ext := &fakeExtractor{
		classifyResults: []string{"engineering"},
		extractResults:  []string{"no front matter here", validOutput},
	}
	d := &fakeDestination{name: "confluence", url: "https://c/1"}
	store := newFakeStore()

	svc := New([]Source{src}, ext, []Destination{d}, store, testConfig())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(ext.extractCalls) != 2 {
		t.Fatalf("extract calls = %d, want 2 (initial + corrective retry)", len(ext.extractCalls))
	}
	if !strings.Contains(ext.extractCalls[1], "could not be parsed") {
		t.Errorf("retry prompt should explain the parse failure, got: %s", ext.extractCalls[1])
	}
	if _, ok := store.processed["fake:t1"]; !ok {
		t.Error("successful retry should mark transcript processed")
	}
}

func TestExtractMalformedTwiceMarksFailed(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	ext := &fakeExtractor{classifyResults: []string{"engineering"}, extractResults: []string{"junk", "more junk"}}
	store := newFakeStore()

	svc := New([]Source{src}, ext, []Destination{&fakeDestination{name: "confluence"}}, store, testConfig())
	_ = svc.Run(context.Background())

	if _, ok := store.failed["fake:t1"]; !ok {
		t.Error("transcript must be marked failed after two malformed extractions")
	}
}

func TestRunSkipsProcessedAndDryRun(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1"), testTranscript("t2")}}
	ext := &fakeExtractor{classifyResults: []string{"engineering"}, extractResults: []string{validOutput}}
	d := &fakeDestination{name: "confluence", url: "https://c/1"}
	store := newFakeStore()
	store.processed["fake:t1"] = "{}" // already done

	cfg := testConfig()
	cfg.DryRun = true
	svc := New([]Source{src}, ext, []Destination{d}, store, cfg)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(d.published) != 0 {
		t.Errorf("dry run must not publish, got %d publishes", len(d.published))
	}
	if len(store.processed) != 1 {
		t.Errorf("dry run must not mark new transcripts processed, processed=%v", store.processed)
	}
}

func TestRunContinuesWhenOneSourceFails(t *testing.T) {
	bad := &fakeSource{name: "bad", err: errors.New("api down")}
	good := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	ext := &fakeExtractor{classifyResults: []string{"engineering"}, extractResults: []string{validOutput}}
	d := &fakeDestination{name: "confluence", url: "https://c/1"}
	store := newFakeStore()

	svc := New([]Source{bad, good}, ext, []Destination{d}, store, testConfig())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("one failing source must not abort the run: %v", err)
	}
	if _, ok := store.processed["fake:t1"]; !ok {
		t.Error("healthy source's transcript should still be processed")
	}
}
