package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/jgavinray/omniscient/internal/drive"
	"github.com/jgavinray/omniscient/internal/models"
)

// ---------------------------------------------------------------------------
// Fake implementations
// ---------------------------------------------------------------------------

// fakeFetcher returns a fixed set of transcripts.
type fakeFetcher struct {
	transcripts []*drive.Transcript
	err         error
}

func (f *fakeFetcher) GetRecentTranscripts(ctx context.Context, folderID string, since time.Duration) ([]*drive.Transcript, error) {
	return f.transcripts, f.err
}

// fakeExtractor returns a fixed meeting type and extraction output.
type fakeExtractor struct {
	meetingType string
	extractErr  error
}

func (f *fakeExtractor) Classify(ctx context.Context, preview string, templateKeys []string, classifyPrompt string) (string, error) {
	return f.meetingType, nil
}

func (f *fakeExtractor) Extract(ctx context.Context, transcript string, extractionPrompt string) (string, error) {
	return sampleExtractionOutput, f.extractErr
}

// fakePublisher records calls and can return an error.
type fakePublisher struct {
	published []string
	err       error
}

func (f *fakePublisher) PublishMarkdown(ctx context.Context, spaceKey, parentPageID string, result *models.ExtractionResult, transcriptName string) (string, error) {
	f.published = append(f.published, transcriptName)
	return "https://confluence.example.com/pages/12345", f.err
}

// fakeStore records calls and can return an error.
type fakeStore struct {
	processed        map[string]bool
	processedErr     error
	failed           []string
	failedErr        error
	markProcessedErr error
	markFailedErr    error
	events           []*database.SyncEvent
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		processed: make(map[string]bool),
	}
}

func (s *fakeStore) IsProcessed(ctx context.Context, transcriptID string) (bool, error) {
	return s.processed[transcriptID], s.processedErr
}

func (s *fakeStore) MarkProcessed(ctx context.Context, transcriptID, transcriptName, confluenceURL string) error {
	s.processed[transcriptID] = true
	return s.markProcessedErr
}

func (s *fakeStore) MarkFailed(ctx context.Context, transcriptID, transcriptName, errorMessage string) error {
	s.failed = append(s.failed, transcriptID)
	return s.failedErr
}

func (s *fakeStore) RecordSyncEvent(ctx context.Context, event *database.SyncEvent) error {
	s.events = append(s.events, event)
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeTestConfig() *config.Config {
	return &config.Config{
		Google: config.GoogleConfig{
			CredentialsFile: "/opt/omiscient/credentials.json",
			TokenFile:       "/opt/omiscient/token.json",
			FolderID:        "abc123folderID",
		},
		LLM: config.LLMConfig{
			Provider:      "openai-compatible",
			OpenAIBaseURL: "http://localhost:11434/v1",
			OpenAIAPIKey:  "test-key",
			Model:         "test-model",
			Timeout:       30,
		},
		Confluence: config.ConfluenceConfig{
			BaseURL:      "https://wiki.example.com",
			Email:        "user@example.com",
			APIToken:     "token-abc",
			SpaceKey:     "ENG",
			ParentPageID: "12345",
		},
		Sync: config.SyncConfig{
			LookbackHours: 24,
			DatabasePath:  "/tmp/test.db",
			MaxPerRun:     50,
		},
		Logging: config.LoggingConfig{
			Level: "info",
		},
		Prompts: config.PromptsConfig{
			ClassifyPrompt: "Classify meeting: {{TEMPLATE_KEYS}} {{TRANSCRIPT_PREVIEW}}",
			Templates: map[string]config.MeetingTemplate{
				"engineering": {
					Description:      "Engineering standups",
					ExtractionPrompt: "Extract notes: {{TRANSCRIPT}}",
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Acceptance tests
// ---------------------------------------------------------------------------

func TestSyncService_AlreadyPublishedSkip(t *testing.T) {
	store := newFakeStore()
	store.processed["doc-001"] = true

	fetcher := &fakeFetcher{
		transcripts: []*drive.Transcript{
			{ID: "doc-001", Name: "Team Standup.gdoc", Content: "Alice: I worked on the pipeline."},
		},
	}
	extractor := &fakeExtractor{meetingType: "engineering"}
	publisher := &fakePublisher{}
	cfg := makeTestConfig()

	svc := NewSyncService(fetcher, extractor, publisher, store, cfg)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// The transcript was already processed, so nothing should be published.
	if len(publisher.published) != 0 {
		t.Errorf("expected 0 published, got %d", len(publisher.published))
	}
}

func TestSyncService_DryRunNoPublishNoMarkProcessed(t *testing.T) {
	store := newFakeStore()

	fetcher := &fakeFetcher{
		transcripts: []*drive.Transcript{
			{ID: "doc-001", Name: "Team Standup.gdoc", Content: "Alice: I worked on the pipeline."},
		},
	}
	extractor := &fakeExtractor{meetingType: "engineering"}
	publisher := &fakePublisher{}
	cfg := makeTestConfig()
	cfg.DryRun = true

	svc := NewSyncService(fetcher, extractor, publisher, store, cfg)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// In dry-run mode, nothing should be published.
	if len(publisher.published) != 0 {
		t.Errorf("expected 0 published in dry-run, got %d", len(publisher.published))
	}

	// In dry-run mode, MarkProcessed should NOT be called (the transcript
	// is not marked as processed).
	if store.processed["doc-001"] {
		t.Error("expected doc-001 NOT to be marked as processed in dry-run mode")
	}
}

func TestSyncService_ConfluenceDisabledNoMarkProcessed(t *testing.T) {
	store := newFakeStore()

	fetcher := &fakeFetcher{
		transcripts: []*drive.Transcript{
			{ID: "doc-001", Name: "Team Standup.gdoc", Content: "Alice: I worked on the pipeline."},
		},
	}
	extractor := &fakeExtractor{meetingType: "engineering"}
	publisher := &fakePublisher{}
	cfg := makeTestConfig()
	cfg.Confluence.Enabled = boolPtr(false)

	svc := NewSyncService(fetcher, extractor, publisher, store, cfg)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Confluence disabled: no publish calls.
	if len(publisher.published) != 0 {
		t.Errorf("expected 0 published, got %d", len(publisher.published))
	}

	// MarkProcessed should NOT be called when confluence is disabled.
	if store.processed["doc-001"] {
		t.Error("expected doc-001 NOT to be marked as processed when confluence is disabled")
	}
}

func TestSyncService_ExtractionFailureCallsMarkFailed(t *testing.T) {
	store := newFakeStore()

	fetcher := &fakeFetcher{
		transcripts: []*drive.Transcript{
			{ID: "doc-001", Name: "Team Standup.gdoc", Content: "Alice: I worked on the pipeline."},
		},
	}
	extractor := &fakeExtractor{meetingType: "engineering", extractErr: errors.New("llm timeout")}
	publisher := &fakePublisher{}
	cfg := makeTestConfig()

	svc := NewSyncService(fetcher, extractor, publisher, store, cfg)
	_ = svc.Run(context.Background())

	// MarkFailed should have been called for the failed transcript.
	if len(store.failed) != 1 {
		t.Fatalf("expected 1 MarkFailed call, got %d", len(store.failed))
	}
	if store.failed[0] != "doc-001" {
		t.Errorf("expected MarkFailed for doc-001, got %s", store.failed[0])
	}
}

func TestSyncService_PublishFailureCallsMarkFailed(t *testing.T) {
	store := newFakeStore()

	fetcher := &fakeFetcher{
		transcripts: []*drive.Transcript{
			{ID: "doc-001", Name: "Team Standup.gdoc", Content: "Alice: I worked on the pipeline."},
		},
	}
	extractor := &fakeExtractor{meetingType: "engineering"}
	publisher := &fakePublisher{err: errors.New("confluence unavailable")}
	cfg := makeTestConfig()

	svc := NewSyncService(fetcher, extractor, publisher, store, cfg)
	_ = svc.Run(context.Background())

	// MarkFailed should have been called for the failed transcript.
	if len(store.failed) != 1 {
		t.Fatalf("expected 1 MarkFailed call, got %d", len(store.failed))
	}
	if store.failed[0] != "doc-001" {
		t.Errorf("expected MarkFailed for doc-001, got %s", store.failed[0])
	}

	// Check that store.events contains a SyncEvent with Stage "publish_failed", Status "error",
	// TranscriptID "doc-001", and Metadata containing "confluence unavailable" but not transcript content.
	var publishFailedEvent *database.SyncEvent
	for _, event := range store.events {
		if event.Stage == "publish_failed" {
			publishFailedEvent = event
			break
		}
	}

	if publishFailedEvent == nil {
		t.Fatal("expected publish_failed event not found")
	}

	// Check the specific event properties
	if publishFailedEvent.Status != "error" {
		t.Errorf("expected Status 'error', got '%s'", publishFailedEvent.Status)
	}
	if publishFailedEvent.TranscriptID != "doc-001" {
		t.Errorf("expected TranscriptID 'doc-001', got '%s'", publishFailedEvent.TranscriptID)
	}

	// Parse metadata to check for "confluence unavailable" but not transcript content
	if publishFailedEvent.MetadataJSON != "" {
		// Simple check for the presence of the error message
		if !strings.Contains(publishFailedEvent.MetadataJSON, "confluence unavailable") {
			t.Error("expected metadata to contain 'confluence unavailable'")
		}
		if strings.Contains(publishFailedEvent.MetadataJSON, "Alice: I worked on the pipeline") {
			t.Error("expected metadata to NOT contain transcript content")
		}
	}
}

func TestSyncService_MarkProcessedFailureReturnsPersistenceError(t *testing.T) {
	store := newFakeStore()
	store.markProcessedErr = errors.New("sqlite disk I/O")

	fetcher := &fakeFetcher{
		transcripts: []*drive.Transcript{
			{ID: "doc-001", Name: "Team Standup.gdoc", Content: "Alice: I worked on the pipeline."},
		},
	}
	extractor := &fakeExtractor{meetingType: "engineering"}
	publisher := &fakePublisher{}
	cfg := makeTestConfig()

	svc := NewSyncService(fetcher, extractor, publisher, store, cfg)
	err := svc.Run(context.Background())
	if err == nil {
		t.Fatal("expected error due to MarkProcessed failure")
	}
	if !strings.Contains(err.Error(), "persist state") {
		t.Errorf("expected error to mention persist state, got: %v", err)
	}
}

func TestSyncService_AllNonSkippedTranscriptsFailPublishReturnsError(t *testing.T) {
	store := newFakeStore()

	fetcher := &fakeFetcher{
		transcripts: []*drive.Transcript{
			{ID: "doc-001", Name: "Team Standup.gdoc", Content: "Alice: I worked on the pipeline."},
			{ID: "doc-002", Name: "Engineering Review.gdoc", Content: "Bob: The pipeline is working."},
			{ID: "doc-003", Name: "Planning Meeting.gdoc", Content: "Carol: We need to improve reliability."},
		},
	}
	extractor := &fakeExtractor{meetingType: "engineering"}
	publisher := &fakePublisher{err: errors.New("confluence unavailable")}
	cfg := makeTestConfig()

	svc := NewSyncService(fetcher, extractor, publisher, store, cfg)
	err := svc.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when all non-skipped transcripts fail publish")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("expected error message to contain 'failed', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func boolPtr(b bool) *bool { return &b }

func TestSyncService_SuccessfulRunRecordsAllEventStages(t *testing.T) {
	store := newFakeStore()

	fetcher := &fakeFetcher{
		transcripts: []*drive.Transcript{
			{ID: "doc-001", Name: "Team Standup.gdoc", Content: "Alice: I worked on the pipeline."},
		},
	}
	extractor := &fakeExtractor{meetingType: "engineering"}
	publisher := &fakePublisher{}
	cfg := makeTestConfig()

	svc := NewSyncService(fetcher, extractor, publisher, store, cfg)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	wantStages := []string{
		"run_started",
		"transcripts_fetched",
		"transcript_discovered",
		"classification_succeeded",
		"extraction_succeeded",
		"publish_succeeded",
		"state_persistence_succeeded",
		"run_completed",
	}
	if len(store.events) != len(wantStages) {
		t.Fatalf("expected %d events, got %d", len(wantStages), len(store.events))
	}
	for i, want := range wantStages {
		if store.events[i].Stage != want {
			t.Errorf("event[%d].Stage = %q, want %q", i, store.events[i].Stage, want)
		}
	}
}
