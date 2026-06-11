package database

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// newTestStore creates a Store backed by a SQLite database inside
// the test's temporary directory. It calls t.Fatal on error and
// registers a cleanup function to close the store when the test finishes.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore(%s): %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close(): %v", err)
		}
	})
	return store
}

func TestNewStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore returned unexpected error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned unexpected error: %v", err)
	}
}

func TestIsProcessed_EmptyDB(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	processed, err := store.IsProcessed(ctx, "nonexistent-id")
	if err != nil {
		t.Fatalf("IsProcessed returned unexpected error: %v", err)
	}
	if processed {
		t.Error("IsProcessed returned true for empty database, want false")
	}
}

func TestMarkProcessed_ThenIsProcessed(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	const (
		id   = "transcript-001"
		name = "Weekly Standup 2026-02-21"
		url  = "https://confluence.example.com/pages/12345"
	)

	if err := store.MarkProcessed(ctx, id, name, url); err != nil {
		t.Fatalf("MarkProcessed returned unexpected error: %v", err)
	}

	processed, err := store.IsProcessed(ctx, id)
	if err != nil {
		t.Fatalf("IsProcessed returned unexpected error: %v", err)
	}
	if !processed {
		t.Error("IsProcessed returned false after MarkProcessed, want true")
	}
}

func TestMarkProcessed_Idempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	const (
		id   = "transcript-dup"
		name = "Retro Meeting"
		url  = "https://confluence.example.com/pages/99999"
	)

	if err := store.MarkProcessed(ctx, id, name, url); err != nil {
		t.Fatalf("first MarkProcessed returned unexpected error: %v", err)
	}
	if err := store.MarkProcessed(ctx, id, name, url); err != nil {
		t.Fatalf("second MarkProcessed returned unexpected error: %v", err)
	}

	processed, err := store.IsProcessed(ctx, id)
	if err != nil {
		t.Fatalf("IsProcessed returned unexpected error: %v", err)
	}
	if !processed {
		t.Error("IsProcessed returned false after duplicate MarkProcessed, want true")
	}
}

func TestMultipleTranscripts(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	transcripts := []struct {
		id   string
		name string
		url  string
	}{
		{"t-aaa", "Sprint Planning", "https://confluence.example.com/pages/1"},
		{"t-bbb", "Design Review", "https://confluence.example.com/pages/2"},
		{"t-ccc", "Incident Postmortem", "https://confluence.example.com/pages/3"},
	}

	for _, tr := range transcripts {
		if err := store.MarkProcessed(ctx, tr.id, tr.name, tr.url); err != nil {
			t.Fatalf("MarkProcessed(%s) returned unexpected error: %v", tr.id, err)
		}
	}

	for _, tr := range transcripts {
		processed, err := store.IsProcessed(ctx, tr.id)
		if err != nil {
			t.Fatalf("IsProcessed(%s) returned unexpected error: %v", tr.id, err)
		}
		if !processed {
			t.Errorf("IsProcessed(%s) = false, want true", tr.id)
		}
	}

	// Verify an ID that was never inserted is still not found.
	processed, err := store.IsProcessed(ctx, "t-zzz-never-added")
	if err != nil {
		t.Fatalf("IsProcessed(unknown) returned unexpected error: %v", err)
	}
	if processed {
		t.Error("IsProcessed returned true for unknown transcript, want false")
	}
}

func TestNewStore_CreatesDirectories(t *testing.T) {
	// Build a nested path that does not yet exist inside the temp dir.
	dbPath := filepath.Join(t.TempDir(), "a", "b", "c", "test.db")
	ctx := context.Background()

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore with nested path returned unexpected error: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close returned unexpected error: %v", err)
		}
	}()

	// Smoke-test that the database is functional.
	if err := store.MarkProcessed(ctx, "nested-id", "Nested Test", "https://example.com"); err != nil {
		t.Fatalf("MarkProcessed on nested-path store returned unexpected error: %v", err)
	}
	processed, err := store.IsProcessed(ctx, "nested-id")
	if err != nil {
		t.Fatalf("IsProcessed on nested-path store returned unexpected error: %v", err)
	}
	if !processed {
		t.Error("IsProcessed returned false on nested-path store, want true")
	}
}

func TestMarkProcessed_GetTranscriptPublishedFields(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	id := "transcript-123"
	name := "Q2 Planning Notes"
	url := "https://company.atlassian.net/wiki/spaces/TEAM/pages/123456789/Q2+Planning"

	err := store.MarkProcessed(ctx, id, name, url)
	if err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	rec, err := store.GetTranscript(ctx, id)
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}

	if rec.Status != "published" {
		t.Errorf("Status = %q, want %q", rec.Status, "published")
	}

	if !rec.ConfluenceURL.Valid {
		t.Fatal("ConfluenceURL.Valid == false, want true")
	}
	if rec.ConfluenceURL.String != url {
		t.Errorf("ConfluenceURL.String = %q, want %q", rec.ConfluenceURL.String, url)
	}

	if !rec.PublishedAt.Valid {
		t.Fatal("PublishedAt.Valid == false, want true")
	}
	if rec.PublishedAt.Time.IsZero() {
		t.Fatal("PublishedAt.Time.IsZero() == true, want false")
	}

	if rec.LastError.Valid {
		t.Errorf("LastError.Valid == true, want false")
	}
}

func TestMarkFailed_IncrementsAttemptsAndStoresLastError(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	id := "transcript-failed"
	name := "Failed Meeting"

	err := store.MarkFailed(ctx, id, name, "first error")
	if err != nil {
		t.Fatalf("MarkFailed first call failed: %v", err)
	}

	processed, err := store.IsProcessed(ctx, id)
	if err != nil {
		t.Fatalf("IsProcessed after first MarkFailed failed: %v", err)
	}
	if processed {
		t.Fatalf("IsProcessed should be false after MarkFailed, got true")
	}

	err = store.MarkFailed(ctx, id, name, "second error")
	if err != nil {
		t.Fatalf("MarkFailed second call failed: %v", err)
	}

	processed, err = store.IsProcessed(ctx, id)
	if err != nil {
		t.Fatalf("IsProcessed after second MarkFailed failed: %v", err)
	}
	if processed {
		t.Fatalf("IsProcessed should be false after MarkFailed, got true")
	}

	record, err := store.GetTranscript(ctx, id)
	if err != nil {
		t.Fatalf("GetTranscript failed: %v", err)
	}

	if record.Status != StatusFailed {
		t.Fatalf("Status should be StatusFailed, got %s", record.Status)
	}

	if record.AttemptCount != 2 {
		t.Fatalf("AttemptCount should be 2, got %d", record.AttemptCount)
	}

	if !record.LastError.Valid {
		t.Fatalf("LastError.Valid should be true, got false")
	}

	if record.LastError.String != "second error" {
		t.Fatalf("LastError.String should be 'second error', got '%s'", record.LastError.String)
	}
}

func TestLifecycleStatuses_NotProcessed(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	tests := []struct {
		name       string
		id         string
		wantStatus string
		mark       func() error
	}{
		{
			name:       "discovered",
			id:         "disc-001",
			wantStatus: StatusDiscovered,
			mark: func() error {
				return store.MarkDiscovered(ctx, "disc-001", "Meeting Discovery")
			},
		},
		{
			name:       "extracted",
			id:         "ext-001",
			wantStatus: StatusExtracted,
			mark: func() error {
				return store.MarkExtracted(ctx, "ext-001", "Meeting Extraction")
			},
		},
		{
			name:       "skipped",
			id:         "skip-001",
			wantStatus: StatusSkipped,
			mark: func() error {
				return store.MarkSkipped(ctx, "skip-001", "Meeting Skipped", "not a meeting")
			},
		},
		{
			name:       "failed",
			id:         "fail-001",
			wantStatus: StatusFailed,
			mark: func() error {
				return store.MarkFailed(ctx, "fail-001", "Meeting Failed", "extraction timeout")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mark(); err != nil {
				t.Fatalf("mark: %v", err)
			}

			processed, err := store.IsProcessed(ctx, tc.id)
			if err != nil {
				t.Fatalf("IsProcessed(%s): %v", tc.id, err)
			}
			if processed {
				t.Fatalf("IsProcessed(%s) = true, want false", tc.id)
			}

			rec, err := store.GetTranscript(ctx, tc.id)
			if err != nil {
				t.Fatalf("GetTranscript(%s): %v", tc.id, err)
			}
			if rec == nil {
				t.Fatalf("GetTranscript(%s) returned nil record", tc.id)
			}
			if rec.Status != tc.wantStatus {
				t.Fatalf("GetTranscript(%s).Status = %s, want %s", tc.id, rec.Status, tc.wantStatus)
			}
		})
	}
}

func TestNewStore_MigratesOldSchemaAsPublished(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE processed_transcripts (
			transcript_id   TEXT PRIMARY KEY,
			transcript_name TEXT NOT NULL,
			processed_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			confluence_url  TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	_, err = db.Exec(
		`INSERT INTO processed_transcripts (transcript_id, transcript_name, processed_at, confluence_url)
		 VALUES ('old-1', 'Legacy Meeting', '2026-01-02 03:04:05', 'https://confluence.example.com/legacy')`,
	)
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	ok, err := store.IsProcessed(ctx, "googlemeet:old-1")
	if err != nil {
		t.Fatalf("IsProcessed: %v", err)
	}
	if !ok {
		t.Fatal("IsProcessed('googlemeet:old-1') = false, want true")
	}

	rec, err := store.GetTranscript(ctx, "googlemeet:old-1")
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if rec == nil {
		t.Fatal("GetTranscript returned nil")
	}
	if rec.Status != StatusPublished {
		t.Errorf("Status = %s, want %s", rec.Status, StatusPublished)
	}
	if !rec.ConfluenceURL.Valid {
		t.Fatal("ConfluenceURL.Valid = false, want true")
	}
	if rec.ConfluenceURL.String != "https://confluence.example.com/legacy" {
		t.Errorf("ConfluenceURL.String = %q, want %q", rec.ConfluenceURL.String, "https://confluence.example.com/legacy")
	}
	if !rec.PublishedAt.Valid {
		t.Fatal("PublishedAt.Valid = false, want true")
	}
	if rec.PublishedAt.Time.IsZero() {
		t.Fatal("PublishedAt.Time.IsZero() = true, want false")
	}
}

func TestStatusCounts_EmptyDB(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	counts, err := store.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts: %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("StatusCounts returned %d entries, want 0", len(counts))
	}
}

func TestStatusCounts_InsertedStatuses(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Insert rows with different statuses.
	if err := store.MarkDiscovered(ctx, "disc-1", "Meeting A"); err != nil {
		t.Fatalf("MarkDiscovered: %v", err)
	}
	if err := store.MarkExtracted(ctx, "ext-1", "Meeting B"); err != nil {
		t.Fatalf("MarkExtracted: %v", err)
	}
	if err := store.MarkFailed(ctx, "fail-1", "Meeting C", "timeout"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if err := store.MarkSkipped(ctx, "skip-1", "Meeting D", "not a meeting"); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	if err := store.MarkProcessed(ctx, "pub-1", "Meeting E", "https://example.com"); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	counts, err := store.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts: %v", err)
	}

	expected := map[string]int{
		StatusDiscovered: 1,
		StatusExtracted:  1,
		StatusFailed:     1,
		StatusSkipped:    1,
		StatusPublished:  1,
	}
	if len(counts) != len(expected) {
		t.Fatalf("StatusCounts returned %d entries, want %d", len(counts), len(expected))
	}
	for status, want := range expected {
		if got := counts[status]; got != want {
			t.Errorf("StatusCounts[%q] = %d, want %d", status, got, want)
		}
	}
}

func TestRecentTranscripts_DefaultLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Insert 15 transcripts with distinct names so we can verify ordering.
	for i := 0; i < 15; i++ {
		id := fmt.Sprintf("rec-%03d", i)
		name := fmt.Sprintf("Meeting %d", i)
		if err := store.MarkDiscovered(ctx, id, name); err != nil {
			t.Fatalf("MarkDiscovered(%s): %v", id, err)
		}
	}

	records, err := store.RecentTranscripts(ctx, 0) // 0 → default 10
	if err != nil {
		t.Fatalf("RecentTranscripts: %v", err)
	}
	if len(records) != 10 {
		t.Fatalf("RecentTranscripts returned %d records, want 10", len(records))
	}

	// Verify descending order by updated_at.
	for i := 1; i < len(records); i++ {
		if records[i].UpdatedAt.After(records[i-1].UpdatedAt) {
			t.Errorf("Records not in DESC order at index %d", i)
		}
	}
}

func TestRecentTranscripts_CustomLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("rec-%03d", i)
		name := fmt.Sprintf("Meeting %d", i)
		if err := store.MarkDiscovered(ctx, id, name); err != nil {
			t.Fatalf("MarkDiscovered(%s): %v", id, err)
		}
	}

	records, err := store.RecentTranscripts(ctx, 3)
	if err != nil {
		t.Fatalf("RecentTranscripts: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("RecentTranscripts returned %d records, want 3", len(records))
	}
}

func TestRetryFailed_OnlyChangesFailed(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Insert one failed, one discovered, one published.
	if err := store.MarkFailed(ctx, "fail-1", "Meeting A", "timeout"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if err := store.MarkDiscovered(ctx, "disc-1", "Meeting B"); err != nil {
		t.Fatalf("MarkDiscovered: %v", err)
	}
	if err := store.MarkProcessed(ctx, "pub-1", "Meeting C", "https://example.com"); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	affected, err := store.RetryFailed(ctx)
	if err != nil {
		t.Fatalf("RetryFailed: %v", err)
	}
	if affected != 1 {
		t.Fatalf("RetryFailed returned %d, want 1", affected)
	}

	// The failed row should now be discovered with cleared fields.
	rec, err := store.GetTranscript(ctx, "fail-1")
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if rec.Status != StatusDiscovered {
		t.Errorf("Status = %s, want %s", rec.Status, StatusDiscovered)
	}
	if rec.LastError.Valid {
		t.Error("LastError should be NULL after retry, got non-nil")
	}
	if rec.ConfluenceURL.Valid {
		t.Error("ConfluenceURL should be NULL after retry, got non-nil")
	}

	// The discovered row should remain unchanged.
	recDisc, err := store.GetTranscript(ctx, "disc-1")
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if recDisc.Status != StatusDiscovered {
		t.Errorf("disc-1 Status = %s, want %s", recDisc.Status, StatusDiscovered)
	}

	// The published row should remain unchanged.
	recPub, err := store.GetTranscript(ctx, "pub-1")
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if recPub.Status != StatusPublished {
		t.Errorf("pub-1 Status = %s, want %s", recPub.Status, StatusPublished)
	}
}

func TestSyncEvents_QueryByRunID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	events := []SyncEvent{
		{ID: "event-1", RunID: "run-1", TranscriptID: "doc-1", Stage: "run_started", Status: "ok", MetadataJSON: `{}`, CreatedAt: "2026-01-02T03:04:05Z"},
		{ID: "event-2", RunID: "run-1", TranscriptID: "doc-2", Stage: "transcript_discovered", Status: "ok", MetadataJSON: `{}`, CreatedAt: "2026-01-02T03:04:06Z"},
		{ID: "event-3", RunID: "run-2", TranscriptID: "doc-3", Stage: "run_started", Status: "ok", MetadataJSON: `{}`, CreatedAt: "2026-01-02T03:04:07Z"},
	}
	for i := range events {
		if err := store.RecordSyncEvent(ctx, &events[i]); err != nil {
			t.Fatalf("RecordSyncEvent(%s): %v", events[i].ID, err)
		}
	}

	got, err := store.QuerySyncEventsByRunID(ctx, "run-1")
	if err != nil {
		t.Fatalf("QuerySyncEventsByRunID: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("QuerySyncEventsByRunID returned %d events, want 2", len(got))
	}
	for _, event := range got {
		if event.RunID != "run-1" {
			t.Fatalf("QuerySyncEventsByRunID returned event for run %q", event.RunID)
		}
	}
}

func TestSyncEvents_QueryByTranscriptID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	events := []SyncEvent{
		{ID: "event-1", RunID: "run-1", TranscriptID: "doc-1", Stage: "classification", Status: "ok", MetadataJSON: `{}`, CreatedAt: "2026-01-02T03:04:05Z"},
		{ID: "event-2", RunID: "run-2", TranscriptID: "doc-1", Stage: "extraction", Status: "ok", MetadataJSON: `{}`, CreatedAt: "2026-01-02T03:04:06Z"},
		{ID: "event-3", RunID: "run-1", TranscriptID: "doc-2", Stage: "classification", Status: "ok", MetadataJSON: `{}`, CreatedAt: "2026-01-02T03:04:07Z"},
	}
	for i := range events {
		if err := store.RecordSyncEvent(ctx, &events[i]); err != nil {
			t.Fatalf("RecordSyncEvent(%s): %v", events[i].ID, err)
		}
	}

	got, err := store.QuerySyncEventsByTranscriptID(ctx, "doc-1")
	if err != nil {
		t.Fatalf("QuerySyncEventsByTranscriptID: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("QuerySyncEventsByTranscriptID returned %d events, want 2", len(got))
	}
	for _, event := range got {
		if event.TranscriptID != "doc-1" {
			t.Fatalf("QuerySyncEventsByTranscriptID returned event for transcript %q", event.TranscriptID)
		}
	}
}

func TestTruncateMetadata_BoundsKeysAndValues(t *testing.T) {
	metadata := make(map[string]string)
	longValue := "abcdefghijklmnopqrstuvwxyz"
	for i := 0; i < MaxMetadataKeys+5; i++ {
		metadata[fmt.Sprintf("key-%02d", i)] = longValue
	}
	metadata["long"] = string(make([]byte, MaxMetadataValueLen+10))

	got := TruncateMetadata(metadata)
	if len(got) > MaxMetadataKeys {
		t.Fatalf("TruncateMetadata returned %d keys, want at most %d", len(got), MaxMetadataKeys)
	}
	for key, value := range got {
		if len(value) > MaxMetadataValueLen {
			t.Fatalf("TruncateMetadata[%s] length = %d, want at most %d", key, len(value), MaxMetadataValueLen)
		}
	}
}

func TestNewStore_LegacyMigrationCreatesSyncEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-events.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE processed_transcripts (
			transcript_id   TEXT PRIMARY KEY,
			transcript_name TEXT NOT NULL,
			processed_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			confluence_url  TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	event := &SyncEvent{ID: "event-legacy", RunID: "run-legacy", TranscriptID: "doc-legacy", Stage: "run_started", Status: "ok", MetadataJSON: `{}`, CreatedAt: "2026-01-02T03:04:05Z"}
	if err := store.RecordSyncEvent(ctx, event); err != nil {
		t.Fatalf("RecordSyncEvent after legacy migration: %v", err)
	}
	got, err := store.QuerySyncEventsByRunID(ctx, "run-legacy")
	if err != nil {
		t.Fatalf("QuerySyncEventsByRunID after legacy migration: %v", err)
	}
	if len(got) != 1 || got[0].ID != "event-legacy" {
		t.Fatalf("legacy sync_events query returned %#v, want event-legacy", got)
	}
}

func TestMigrationV2PrefixesUnprefixedKeys(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// First open creates the current schema; insert a legacy (unprefixed) row.
	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.MarkProcessed(ctx, "abc123", "Old Meeting", "https://x/page"); err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-v2 database: clear the version stamp.
	if _, err := s.db.Exec(`DELETE FROM schema_version`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Re-open: v2 migration must prefix the key.
	s2, err := NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	processed, err := s2.IsProcessed(ctx, "googlemeet:abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !processed {
		t.Error("expected googlemeet:abc123 to be processed after v2 migration")
	}
	processed, err = s2.IsProcessed(ctx, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if processed {
		t.Error("unprefixed key abc123 should no longer exist after migration")
	}
}
