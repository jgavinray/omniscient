package database

import (
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

	processed, err := store.IsProcessed("nonexistent-id")
	if err != nil {
		t.Fatalf("IsProcessed returned unexpected error: %v", err)
	}
	if processed {
		t.Error("IsProcessed returned true for empty database, want false")
	}
}

func TestMarkProcessed_ThenIsProcessed(t *testing.T) {
	store := newTestStore(t)

	const (
		id   = "transcript-001"
		name = "Weekly Standup 2026-02-21"
		url  = "https://confluence.example.com/pages/12345"
	)

	if err := store.MarkProcessed(id, name, url); err != nil {
		t.Fatalf("MarkProcessed returned unexpected error: %v", err)
	}

	processed, err := store.IsProcessed(id)
	if err != nil {
		t.Fatalf("IsProcessed returned unexpected error: %v", err)
	}
	if !processed {
		t.Error("IsProcessed returned false after MarkProcessed, want true")
	}
}

func TestMarkProcessed_Idempotent(t *testing.T) {
	store := newTestStore(t)

	const (
		id   = "transcript-dup"
		name = "Retro Meeting"
		url  = "https://confluence.example.com/pages/99999"
	)

	if err := store.MarkProcessed(id, name, url); err != nil {
		t.Fatalf("first MarkProcessed returned unexpected error: %v", err)
	}
	if err := store.MarkProcessed(id, name, url); err != nil {
		t.Fatalf("second MarkProcessed returned unexpected error: %v", err)
	}

	processed, err := store.IsProcessed(id)
	if err != nil {
		t.Fatalf("IsProcessed returned unexpected error: %v", err)
	}
	if !processed {
		t.Error("IsProcessed returned false after duplicate MarkProcessed, want true")
	}
}

func TestMultipleTranscripts(t *testing.T) {
	store := newTestStore(t)

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
		if err := store.MarkProcessed(tr.id, tr.name, tr.url); err != nil {
			t.Fatalf("MarkProcessed(%s) returned unexpected error: %v", tr.id, err)
		}
	}

	for _, tr := range transcripts {
		processed, err := store.IsProcessed(tr.id)
		if err != nil {
			t.Fatalf("IsProcessed(%s) returned unexpected error: %v", tr.id, err)
		}
		if !processed {
			t.Errorf("IsProcessed(%s) = false, want true", tr.id)
		}
	}

	// Verify an ID that was never inserted is still not found.
	processed, err := store.IsProcessed("t-zzz-never-added")
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
	if err := store.MarkProcessed("nested-id", "Nested Test", "https://example.com"); err != nil {
		t.Fatalf("MarkProcessed on nested-path store returned unexpected error: %v", err)
	}
	processed, err := store.IsProcessed("nested-id")
	if err != nil {
		t.Fatalf("IsProcessed on nested-path store returned unexpected error: %v", err)
	}
	if !processed {
		t.Error("IsProcessed returned false on nested-path store, want true")
	}
}
