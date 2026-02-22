package database

import (
	"context"
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
