package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store provides SQLite-backed tracking of processed transcripts
// to ensure idempotent pipeline runs.
type Store struct {
	db *sql.DB
}

// NewStore opens (or creates) a SQLite database at dbPath, ensures the
// parent directory exists, creates the schema if needed, and enables WAL mode.
func NewStore(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create database directory %s: %w", dir, err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", dbPath, err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database %s: %w", dbPath, err)
	}

	schema := `
		CREATE TABLE IF NOT EXISTS processed_transcripts (
			transcript_id   TEXT PRIMARY KEY,
			transcript_name TEXT NOT NULL,
			processed_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			confluence_url  TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_processed_at ON processed_transcripts(processed_at);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	return &Store{db: db}, nil
}

// IsProcessed reports whether a transcript with the given ID has already
// been processed.
func (s *Store) IsProcessed(transcriptID string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM processed_transcripts WHERE transcript_id = ?)",
		transcriptID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check processed status for %s: %w", transcriptID, err)
	}
	return exists, nil
}

// MarkProcessed records a transcript as processed. The operation is
// idempotent — re-inserting an existing transcript_id is silently ignored.
// A transaction is used for atomicity.
func (s *Store) MarkProcessed(transcriptID, transcriptName, confluenceURL string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		"INSERT OR IGNORE INTO processed_transcripts (transcript_id, transcript_name, confluence_url) VALUES (?, ?, ?)",
		transcriptID, transcriptName, confluenceURL,
	)
	if err != nil {
		return fmt.Errorf("mark transcript %s as processed: %w", transcriptID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
