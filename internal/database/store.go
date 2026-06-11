package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Lifecycle status constants for transcript processing pipeline.
const (
	StatusDiscovered = "discovered"
	StatusExtracted  = "extracted"
	StatusPublished  = "published"
	StatusSkipped    = "skipped"
	StatusFailed     = "failed"
)

// TranscriptRecord represents a transcript lifecycle record in the database.
type TranscriptRecord struct {
	TranscriptID   string         `json:"transcript_id"`
	TranscriptName string         `json:"transcript_name"`
	Status         string         `json:"status"`
	ConfluenceURL  sql.NullString `json:"confluence_url"`
	LastError      sql.NullString `json:"last_error"`
	AttemptCount   int            `json:"attempt_count"`
	FirstSeenAt    time.Time      `json:"first_seen_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	PublishedAt    sql.NullTime   `json:"published_at"`
}

// Store provides SQLite-backed tracking of processed transcripts
// to ensure idempotent pipeline runs.
type Store struct {
	db *sql.DB
}

// NewStore opens (or creates) a SQLite database at dbPath, ensures the
// parent directory exists, creates the schema if needed, and enables WAL mode.
func NewStore(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
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

	if err := ensureSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	return &Store{db: db}, nil
}

// SyncEvent represents an append-only sync pipeline event.
type SyncEvent struct {
	ID           string `json:"id"`
	RunID        string `json:"run_id"`
	TranscriptID string `json:"transcript_id,omitempty"`
	Stage        string `json:"stage"`
	Status       string `json:"status"`
	MetadataJSON string `json:"metadata_json"`
	CreatedAt    string `json:"created_at"`
}

// MaxMetadataKeys is the maximum number of keys allowed in the metadata map.
const MaxMetadataKeys = 16

// MaxMetadataValueLen is the maximum length (in bytes) for any metadata value.
const MaxMetadataValueLen = 256

// ensureSchema creates the target schema on a fresh database or migrates
// an old processed_transcripts table (with processed_at and no status) to
// the new lifecycle schema. It does not use ALTER TABLE ADD COLUMN with
// CURRENT_TIMESTAMP defaults.
func ensureSchema(db *sql.DB) error {
	hasTable, err := tableExists(db, "processed_transcripts")
	if err != nil {
		return fmt.Errorf("check processed_transcripts table: %w", err)
	}

	if !hasTable {
		// Fresh database: create the target schema directly.
		schema := `
			CREATE TABLE processed_transcripts (
				transcript_id   TEXT PRIMARY KEY,
				transcript_name TEXT NOT NULL,
				status          TEXT NOT NULL DEFAULT 'discovered',
				confluence_url  TEXT,
				last_error      TEXT,
				attempt_count   INTEGER NOT NULL DEFAULT 0,
				first_seen_at   TEXT NOT NULL,
				updated_at      TEXT NOT NULL,
				published_at    TEXT
			);
			CREATE INDEX idx_status ON processed_transcripts(status);
			CREATE INDEX idx_updated_at ON processed_transcripts(updated_at);

			CREATE TABLE sync_events (
				id             TEXT PRIMARY KEY,
				run_id         TEXT NOT NULL,
				transcript_id  TEXT,
				stage          TEXT NOT NULL,
				status         TEXT NOT NULL,
				metadata_json  TEXT NOT NULL DEFAULT '{}',
				created_at     TEXT NOT NULL
			);
			CREATE INDEX idx_sync_events_run_id ON sync_events(run_id);
			CREATE INDEX idx_sync_events_transcript_id ON sync_events(transcript_id);
		`
		if _, err := db.Exec(schema); err != nil {
			return fmt.Errorf("create target schema: %w", err)
		}
		return ensureVersion(db)
	}

	// Table exists: check whether it has the new status column.
	hasStatus, err := columnExists(db, "processed_transcripts", "status")
	if err != nil {
		return fmt.Errorf("check status column: %w", err)
	}

	if hasStatus {
		// Already on the target schema; ensure sync_events exists too.
		hasSyncEvents, err := tableExists(db, "sync_events")
		if err != nil {
			return fmt.Errorf("check sync_events table: %w", err)
		}
		if !hasSyncEvents {
			syncSchema := `
				CREATE TABLE sync_events (
					id             TEXT PRIMARY KEY,
					run_id         TEXT NOT NULL,
					transcript_id  TEXT,
					stage          TEXT NOT NULL,
					status         TEXT NOT NULL,
					metadata_json  TEXT NOT NULL DEFAULT '{}',
					created_at     TEXT NOT NULL
				);
				CREATE INDEX idx_sync_events_run_id ON sync_events(run_id);
				CREATE INDEX idx_sync_events_transcript_id ON sync_events(transcript_id);
			`
			if _, err := db.Exec(syncSchema); err != nil {
				return fmt.Errorf("create sync_events table: %w", err)
			}
		}
		return ensureVersion(db)
	}

	// Old schema detected: migrate via transaction.
	if err := migrateOldSchema(db); err != nil {
		return err
	}
	return ensureVersion(db)
}

// targetSchemaVersion is bumped whenever a data migration is added.
// Version 2 introduced source-prefixed transcript IDs ("googlemeet:<id>").
const targetSchemaVersion = 2

// ensureVersion creates the schema_version table if needed and runs any
// pending data migrations, stamping the target version when done.
func ensureVersion(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version table: %w", err)
	}

	var version int
	err := db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version)
	if err == sql.ErrNoRows {
		version = 1 // pre-versioning databases are treated as v1
	} else if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if version >= targetSchemaVersion {
		return nil
	}

	if version < 2 {
		if err := migrateToV2(db); err != nil {
			return err
		}
	}

	if _, err := db.Exec(`DELETE FROM schema_version`); err != nil {
		return fmt.Errorf("clear schema version: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, targetSchemaVersion); err != nil {
		return fmt.Errorf("stamp schema version: %w", err)
	}
	return nil
}

// migrateToV2 prefixes legacy transcript IDs with "googlemeet:" — before v2
// the only source was Google Meet via Drive, so all unprefixed IDs belong to
// it. Prefixing keeps multi-source dedup keys collision-free. It is a no-op
// on fresh (empty) databases.
func migrateToV2(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin v2 migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE processed_transcripts SET transcript_id = 'googlemeet:' || transcript_id WHERE transcript_id NOT LIKE '%:%'`); err != nil {
		return fmt.Errorf("prefix processed_transcripts ids: %w", err)
	}
	if _, err := tx.Exec(`UPDATE sync_events SET transcript_id = 'googlemeet:' || transcript_id WHERE transcript_id != '' AND transcript_id NOT LIKE '%:%'`); err != nil {
		return fmt.Errorf("prefix sync_events ids: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit v2 migration: %w", err)
	}
	return nil
}

// migrateOldSchema creates processed_transcripts_new with the target schema,
// copies rows with status='published' and processed_at mapped to
// first_seen_at/updated_at/published_at, drops the old table, and renames
// the new table in place.
func migrateOldSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer tx.Rollback()

	// Create the new table with the target schema.
	_, err = tx.Exec(`
		CREATE TABLE processed_transcripts_new (
			transcript_id   TEXT PRIMARY KEY,
			transcript_name TEXT NOT NULL,
			status          TEXT NOT NULL DEFAULT 'discovered',
			confluence_url  TEXT,
			last_error      TEXT,
			attempt_count   INTEGER NOT NULL DEFAULT 0,
			first_seen_at   TEXT NOT NULL,
			updated_at      TEXT NOT NULL,
			published_at    TEXT
		);
	`)
	if err != nil {
		return fmt.Errorf("create new table: %w", err)
	}

	// Copy rows from the old table. The old table has:
	//   transcript_id TEXT PRIMARY KEY,
	//   transcript_name TEXT NOT NULL,
	//   processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	//   confluence_url TEXT NOT NULL
	// We set status='published', map processed_at to first_seen_at,
	// updated_at, and published_at.
	_, err = tx.Exec(`
		INSERT INTO processed_transcripts_new
			(transcript_id, transcript_name, status, confluence_url, last_error,
			 attempt_count, first_seen_at, updated_at, published_at)
		SELECT
			transcript_id,
			transcript_name,
			'published',
			confluence_url,
			NULL,
			0,
			processed_at,
			processed_at,
			processed_at
		FROM processed_transcripts;
	`)
	if err != nil {
		return fmt.Errorf("copy rows to new table: %w", err)
	}

	// Drop the old table and rename the new one.
	_, err = tx.Exec(`DROP TABLE processed_transcripts`)
	if err != nil {
		return fmt.Errorf("drop old table: %w", err)
	}

	_, err = tx.Exec(`ALTER TABLE processed_transcripts_new RENAME TO processed_transcripts`)
	if err != nil {
		return fmt.Errorf("rename new table: %w", err)
	}

	// Create indexes on the new table.
	_, err = tx.Exec(`CREATE INDEX idx_status ON processed_transcripts(status)`)
	if err != nil {
		return fmt.Errorf("create status index: %w", err)
	}

	_, err = tx.Exec(`CREATE INDEX idx_updated_at ON processed_transcripts(updated_at)`)
	if err != nil {
		return fmt.Errorf("create updated_at index: %w", err)
	}

	_, err = tx.Exec(`
		CREATE TABLE sync_events (
			id             TEXT PRIMARY KEY,
			run_id         TEXT NOT NULL,
			transcript_id  TEXT,
			stage          TEXT NOT NULL,
			status         TEXT NOT NULL,
			metadata_json  TEXT NOT NULL DEFAULT '{}',
			created_at     TEXT NOT NULL
		);
		CREATE INDEX idx_sync_events_run_id ON sync_events(run_id);
		CREATE INDEX idx_sync_events_transcript_id ON sync_events(transcript_id);
	`)
	if err != nil {
		return fmt.Errorf("create sync_events table: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}

	return nil
}

// tableExists returns true if the given table name exists in the database.
func tableExists(db *sql.DB, name string) (bool, error) {
	var cnt int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name,
	).Scan(&cnt)
	if err != nil {
		return false, fmt.Errorf("query sqlite_master: %w", err)
	}
	return cnt > 0, nil
}

// columnExists returns true if the given column exists in the table.
func columnExists(db *sql.DB, tableName, columnName string) (bool, error) {
	var cnt int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?", tableName, columnName,
	).Scan(&cnt)
	if err != nil {
		return false, fmt.Errorf("query pragma_table_info: %w", err)
	}
	return cnt > 0, nil
}

// IsProcessed reports whether a transcript with the given ID has already
// been processed. Returns true only when status = 'published'; missing rows
// return false, nil.
func (s *Store) IsProcessed(ctx context.Context, transcriptID string) (bool, error) {
	var status string
	err := s.db.QueryRowContext(ctx,
		"SELECT status FROM processed_transcripts WHERE transcript_id = ?",
		transcriptID,
	).Scan(&status)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("check processed status for %s: %w", transcriptID, err)
	}
	return status == StatusPublished, nil
}

// MarkProcessed records a transcript as processed. The operation is
// idempotent — re-inserting an existing transcript_id is silently ignored.
// A transaction is used for atomicity.
func (s *Store) MarkProcessed(ctx context.Context, transcriptID, transcriptName, confluenceURL string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx,
		"INSERT INTO processed_transcripts (transcript_id, transcript_name, status, confluence_url, last_error, attempt_count, first_seen_at, updated_at, published_at) VALUES (?, ?, 'published', ?, NULL, 0, ?, ?, ?) ON CONFLICT(transcript_id) DO UPDATE SET transcript_name=excluded.transcript_name, status='published', confluence_url=excluded.confluence_url, updated_at=excluded.updated_at, published_at=excluded.published_at, last_error=NULL",
		transcriptID, transcriptName, confluenceURL, now, now, now,
	)
	if err != nil {
		return fmt.Errorf("mark transcript %s as processed: %w", transcriptID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// parseTime parses a timestamp string, trying RFC3339 first, then the
// SQLite CURRENT_TIMESTAMP format "2006-01-02 15:04:05".
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", s)
}

// MarkDiscovered upserts a transcript with status 'discovered'.
// Rows already 'published' are not downgraded.
func (s *Store) MarkDiscovered(ctx context.Context, transcriptID, transcriptName string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO processed_transcripts (transcript_id, transcript_name, status, first_seen_at, updated_at)
		 VALUES (?, ?, 'discovered', ?, ?)
		 ON CONFLICT(transcript_id) DO UPDATE SET transcript_name=excluded.transcript_name, status='discovered', updated_at=excluded.updated_at
		 WHERE processed_transcripts.status != 'published'`,
		transcriptID, transcriptName, now, now,
	)
	if err != nil {
		return fmt.Errorf("mark transcript %s as discovered: %w", transcriptID, err)
	}
	return nil
}

// MarkExtracted upserts a transcript with status 'extracted'.
// Rows already 'published' are not downgraded.
func (s *Store) MarkExtracted(ctx context.Context, transcriptID, transcriptName string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO processed_transcripts (transcript_id, transcript_name, status, first_seen_at, updated_at)
		 VALUES (?, ?, 'extracted', ?, ?)
		 ON CONFLICT(transcript_id) DO UPDATE SET transcript_name=excluded.transcript_name, status='extracted', updated_at=excluded.updated_at
		 WHERE processed_transcripts.status != 'published'`,
		transcriptID, transcriptName, now, now,
	)
	if err != nil {
		return fmt.Errorf("mark transcript %s as extracted: %w", transcriptID, err)
	}
	return nil
}

// MarkSkipped upserts a transcript with status 'skipped' and stores the reason in last_error.
// Rows already 'published' are not downgraded.
func (s *Store) MarkSkipped(ctx context.Context, transcriptID, transcriptName, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO processed_transcripts (transcript_id, transcript_name, status, last_error, first_seen_at, updated_at)
		 VALUES (?, ?, 'skipped', ?, ?, ?)
		 ON CONFLICT(transcript_id) DO UPDATE SET transcript_name=excluded.transcript_name, status='skipped', last_error=excluded.last_error, updated_at=excluded.updated_at
		 WHERE processed_transcripts.status != 'published'`,
		transcriptID, transcriptName, reason, now, now,
	)
	if err != nil {
		return fmt.Errorf("mark transcript %s as skipped: %w", transcriptID, err)
	}
	return nil
}

// MarkFailed upserts a transcript with status 'failed'. On conflict, increments attempt_count;
// new rows start with attempt_count 1. Rows already 'published' are not downgraded.
func (s *Store) MarkFailed(ctx context.Context, transcriptID, transcriptName, errorMessage string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO processed_transcripts (transcript_id, transcript_name, status, last_error, attempt_count, first_seen_at, updated_at)
		 VALUES (?, ?, 'failed', ?, 1, ?, ?)
		 ON CONFLICT(transcript_id) DO UPDATE SET transcript_name=excluded.transcript_name, status='failed', last_error=excluded.last_error, attempt_count=attempt_count+1, updated_at=excluded.updated_at
		 WHERE processed_transcripts.status != 'published'`,
		transcriptID, transcriptName, errorMessage, now, now,
	)
	if err != nil {
		return fmt.Errorf("mark transcript %s as failed: %w", transcriptID, err)
	}
	return nil
}

// GetTranscript returns the full lifecycle record for a transcript ID.
// Returns nil, nil when the row is missing.
func (s *Store) GetTranscript(ctx context.Context, transcriptID string) (*TranscriptRecord, error) {
	var rec TranscriptRecord
	var firstSeenAtStr, updatedAtStr string
	var publishedAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT transcript_id, transcript_name, status, confluence_url, last_error, attempt_count, first_seen_at, updated_at, published_at
		 FROM processed_transcripts WHERE transcript_id = ?`,
		transcriptID,
	).Scan(
		&rec.TranscriptID,
		&rec.TranscriptName,
		&rec.Status,
		&rec.ConfluenceURL,
		&rec.LastError,
		&rec.AttemptCount,
		&firstSeenAtStr,
		&updatedAtStr,
		&publishedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get transcript %s: %w", transcriptID, err)
	}

	rec.FirstSeenAt, err = parseTime(firstSeenAtStr)
	if err != nil {
		return nil, fmt.Errorf("parse first_seen_at for %s: %w", transcriptID, err)
	}
	rec.UpdatedAt, err = parseTime(updatedAtStr)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at for %s: %w", transcriptID, err)
	}
	if publishedAt.Valid {
		parsed, err := parseTime(publishedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse published_at for %s: %w", transcriptID, err)
		}
		rec.PublishedAt = sql.NullTime{Time: parsed, Valid: true}
	}
	return &rec, nil
}

// StatusCounts returns a map of status → count for all rows in the
// processed_transcripts table. Returns an empty map (not nil) when the
// table has no rows.
func (s *Store) StatusCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM processed_transcripts GROUP BY status`,
	)
	if err != nil {
		return nil, fmt.Errorf("status counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var cnt int
		if err := rows.Scan(&status, &cnt); err != nil {
			return nil, fmt.Errorf("scan status count: %w", err)
		}
		counts[status] = cnt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate status counts: %w", err)
	}
	return counts, nil
}

// RecentTranscripts returns up to limit recent transcript records ordered
// by updated_at DESC. When limit <= 0 it defaults to 10.
func (s *Store) RecentTranscripts(ctx context.Context, limit int) ([]TranscriptRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT transcript_id, transcript_name, status, confluence_url, last_error, attempt_count, first_seen_at, updated_at, published_at
		 FROM processed_transcripts ORDER BY updated_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("recent transcripts: %w", err)
	}
	defer rows.Close()

	var records []TranscriptRecord
	for rows.Next() {
		var rec TranscriptRecord
		var firstSeenAtStr, updatedAtStr string
		var publishedAt sql.NullString
		if err := rows.Scan(
			&rec.TranscriptID,
			&rec.TranscriptName,
			&rec.Status,
			&rec.ConfluenceURL,
			&rec.LastError,
			&rec.AttemptCount,
			&firstSeenAtStr,
			&updatedAtStr,
			&publishedAt,
		); err != nil {
			return nil, fmt.Errorf("scan recent transcript: %w", err)
		}
		rec.FirstSeenAt, err = parseTime(firstSeenAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse first_seen_at: %w", err)
		}
		rec.UpdatedAt, err = parseTime(updatedAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at: %w", err)
		}
		if publishedAt.Valid {
			parsed, err := parseTime(publishedAt.String)
			if err != nil {
				return nil, fmt.Errorf("parse published_at: %w", err)
			}
			rec.PublishedAt = sql.NullTime{Time: parsed, Valid: true}
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent transcripts: %w", err)
	}
	return records, nil
}

// RetryFailed updates every row whose status is 'failed' to 'discovered',
// clearing last_error, confluence_url, published_at and resetting updated_at
// to the current time. Returns the number of rows affected.
func (s *Store) RetryFailed(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE processed_transcripts
		 SET status='discovered', last_error=NULL, confluence_url=NULL, published_at=NULL, updated_at=?
		 WHERE status='failed'`,
		now,
	)
	if err != nil {
		return 0, fmt.Errorf("retry failed: %w", err)
	}
	return res.RowsAffected()
}

// ForgetTranscript deletes the row identified by transcriptID and returns
// the number of rows deleted.
func (s *Store) ForgetTranscript(ctx context.Context, transcriptID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM processed_transcripts WHERE transcript_id = ?`,
		transcriptID,
	)
	if err != nil {
		return 0, fmt.Errorf("forget transcript: %w", err)
	}
	return res.RowsAffected()
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// TruncateMetadata returns a bounded copy of the metadata map where every
// value is truncated to MaxMetadataValueLen bytes and at most
// MaxMetadataKeys keys are kept (in insertion order).
func TruncateMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return metadata
	}
	out := make(map[string]string, len(metadata))
	count := 0
	for k, v := range metadata {
		if count >= MaxMetadataKeys {
			break
		}
		if len(v) > MaxMetadataValueLen {
			v = v[:MaxMetadataValueLen]
		}
		out[k] = v
		count++
	}
	return out
}

// RecordSyncEvent inserts an append-only sync pipeline event row.
func (s *Store) RecordSyncEvent(ctx context.Context, event *SyncEvent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sync_events (id, run_id, transcript_id, stage, status, metadata_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.RunID, event.TranscriptID, event.Stage, event.Status,
		event.MetadataJSON, event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("record sync event: %w", err)
	}
	return nil
}

// QuerySyncEventsByRunID returns all sync events for a given run ID,
// ordered by created_at ASC.
func (s *Store) QuerySyncEventsByRunID(ctx context.Context, runID string) ([]SyncEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, transcript_id, stage, status, metadata_json, created_at
		 FROM sync_events WHERE run_id = ? ORDER BY created_at ASC`,
		runID,
	)
	if err != nil {
		return nil, fmt.Errorf("query sync events by run_id: %w", err)
	}
	defer rows.Close()

	var events []SyncEvent
	for rows.Next() {
		var e SyncEvent
		if err := rows.Scan(&e.ID, &e.RunID, &e.TranscriptID, &e.Stage, &e.Status, &e.MetadataJSON, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan sync event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sync events: %w", err)
	}
	return events, nil
}

// QuerySyncEventsByTranscriptID returns all sync events for a given
// transcript ID, ordered by created_at ASC.
func (s *Store) QuerySyncEventsByTranscriptID(ctx context.Context, transcriptID string) ([]SyncEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, transcript_id, stage, status, metadata_json, created_at
		 FROM sync_events WHERE transcript_id = ? ORDER BY created_at ASC`,
		transcriptID,
	)
	if err != nil {
		return nil, fmt.Errorf("query sync events by transcript_id: %w", err)
	}
	defer rows.Close()

	var events []SyncEvent
	for rows.Next() {
		var e SyncEvent
		if err := rows.Scan(&e.ID, &e.RunID, &e.TranscriptID, &e.Stage, &e.Status, &e.MetadataJSON, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan sync event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sync events: %w", err)
	}
	return events, nil
}
