package main

import (
	"context"
	"fmt"
	"log/slog"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/confluence"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/jgavinray/omniscient/internal/drive"
	"github.com/jgavinray/omniscient/internal/llm"
	"github.com/jgavinray/omniscient/internal/models"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Fetch, extract, and publish recent meeting transcripts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			setupLogging(cfg.Logging.Level, cfg.Logging.File)

			// Set up context with signal handling for graceful shutdown.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return runSync(ctx, cfg)
		},
	}
}

// runSync wires up all dependencies and delegates to the sync service.
func runSync(ctx context.Context, cfg *config.Config) error {
	// Initialize Google Drive client.
	driveClient, err := drive.NewClient(ctx, cfg.Google.CredentialsFile, cfg.Google.TokenFile)
	if err != nil {
		return fmt.Errorf("init drive client: %w", err)
	}

	// Initialize LLM extractor.
	llmExtractor, err := llm.NewExtractor(&cfg.LLM)
	if err != nil {
		return fmt.Errorf("init llm extractor: %w", err)
	}

	// Initialize Confluence publisher (nil when disabled).
	var confluenceClient *confluence.Client
	if cfg.Confluence.IsEnabled() {
		confluenceClient = confluence.NewClient(
			cfg.Confluence.BaseURL,
			cfg.Confluence.Email,
			cfg.Confluence.APIToken,
		)
	}

	// Initialize SQLite database for deduplication.
	store, err := database.NewStore(cfg.Sync.DatabasePath)
	if err != nil {
		return fmt.Errorf("init database: %w", err)
	}
	defer store.Close()

	// Build the service and delegate to it.
	svc := NewSyncService(
		driveClient,
		llmExtractor,
		confluenceClient,
		store,
		cfg,
	)

	return svc.Run(ctx)
}

// --- Sync service ----------------------------------------------------------

// fetcher retrieves transcripts from Google Drive.
type fetcher interface {
	GetRecentTranscripts(ctx context.Context, folderID string, since time.Duration) ([]*drive.Transcript, error)
}

// extractor classifies meeting types and extracts structured notes.
type extractor interface {
	Classify(ctx context.Context, transcriptPreview string, templateKeys []string, classifyPrompt string) (string, error)
	Extract(ctx context.Context, transcript string, extractionPrompt string) (string, error)
}

// publisher publishes extracted notes to Confluence.
type publisher interface {
	PublishMarkdown(ctx context.Context, spaceKey, parentPageID string, result *models.ExtractionResult, transcriptName string) (string, error)
}

// stateStore tracks processed transcripts for idempotent pipeline runs.
type stateStore interface {
	IsProcessed(ctx context.Context, transcriptID string) (bool, error)
	MarkProcessed(ctx context.Context, transcriptID, transcriptName, confluenceURL string) error
	MarkFailed(ctx context.Context, transcriptID, transcriptName, errorMessage string) error
	RecordSyncEvent(ctx context.Context, event *database.SyncEvent) error
}

// SyncService orchestrates the transcript processing pipeline.
type SyncService struct {
	fetcher      fetcher
	extractor    extractor
	publisher    publisher
	store        stateStore
	cfg          *config.Config
	templateKeys []string
	runID        string
	stageCounts  map[string]int
}

// NewSyncService creates a SyncService with the given dependencies.
func NewSyncService(
	fetcher fetcher,
	extractor extractor,
	publisher publisher,
	store stateStore,
	cfg *config.Config,
) *SyncService {
	// Pre-compute sorted template keys.
	templateKeys := make([]string, 0, len(cfg.Prompts.Templates))
	for k := range cfg.Prompts.Templates {
		templateKeys = append(templateKeys, k)
	}
	sort.Strings(templateKeys)

	return &SyncService{
		fetcher:      fetcher,
		extractor:    extractor,
		publisher:    publisher,
		store:        store,
		cfg:          cfg,
		templateKeys: templateKeys,
	}
}

// recordEvent appends a bounded metadata event for the given stage and status.
// On failure it logs a warning with the run ID and continues — observability
// persistence failure must not break the pipeline.
func (s *SyncService) recordEvent(ctx context.Context, stage, status string, metadata map[string]string) {
	if s.stageCounts != nil {
		s.stageCounts[stage]++
	}
	metadataJSON := fmt.Sprintf(`{"stage":"%s","status":"%s"`, stage, status)
	for k, v := range metadata {
		metadataJSON += fmt.Sprintf(`,"%s":"%s"`, k, v)
	}
	metadataJSON += "}"

	// Extract transcript_id from metadata if present for per-transcript events
	var transcriptID string
	if stage != "run_started" && stage != "run_completed" && stage != "transcripts_fetched" {
		if tid, ok := metadata["transcript_id"]; ok {
			transcriptID = tid
		}
	}

	event := &database.SyncEvent{
		ID:           fmt.Sprintf("evt-%s-%s", stage, time.Now().UTC().Format(time.RFC3339Nano)),
		RunID:        s.runID,
		TranscriptID: transcriptID,
		Stage:        stage,
		Status:       status,
		MetadataJSON: metadataJSON,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	if err := s.store.RecordSyncEvent(ctx, event); err != nil {
		slog.Warn("record sync event failed", "run_id", s.runID, "stage", stage, "error", err)
	}
}

// Run executes the full sync pipeline: fetch → classify → extract → parse →
// publish → mark.  It returns early on context cancellation.
func (s *SyncService) Run(ctx context.Context) error {
	s.runID = fmt.Sprintf("run-%s", time.Now().UTC().Format(time.RFC3339Nano))
	s.stageCounts = make(map[string]int)
	slog.Info("sync run started", "run_id", s.runID)
	s.recordEvent(ctx, "run_started", "ok", map[string]string{"status": "started"})
	// Fetch recent transcripts from Google Drive.
	lookback := time.Duration(s.cfg.Sync.LookbackHours) * time.Hour
	transcripts, err := s.fetcher.GetRecentTranscripts(ctx, s.cfg.Google.FolderID, lookback)
	if err != nil {
		return fmt.Errorf("fetch transcripts: %w", err)
	}

	s.recordEvent(ctx, "transcripts_fetched", "ok", map[string]string{"count": fmt.Sprintf("%d", len(transcripts))})
	slog.Info("fetched transcripts", "run_id", s.runID, "count", len(transcripts))

	// Filter out already-processed transcripts.
	var pending []*drive.Transcript
	for _, t := range transcripts {
		processed, err := s.store.IsProcessed(ctx, t.ID)
		if err != nil {
			slog.Warn("check processed failed", "id", t.ID, "error", err)
			continue
		}
		if !processed {
			pending = append(pending, t)
			s.recordEvent(ctx, "transcript_discovered", "ok", map[string]string{"transcript_id": t.ID})
		}
	}

	slog.Info("pending transcripts", "count", len(pending))

	// Limit to max_per_run to prevent runaway processing.
	if len(pending) > s.cfg.Sync.MaxPerRun {
		slog.Warn("limiting transcripts", "pending", len(pending), "max", s.cfg.Sync.MaxPerRun)
		pending = pending[:s.cfg.Sync.MaxPerRun]
	}

	// Process each transcript: classify → extract → parse → publish → mark.
	successCount := 0
	skippedCount := 0
	persistenceFailures := 0
	publishFailures := 0
	for i, transcript := range pending {
		// Check for cancellation between transcripts.
		if err := ctx.Err(); err != nil {
			slog.Warn("sync cancelled", "processed", successCount, "remaining", len(pending)-i)
			break
		}

		slog.Info("processing transcript",
			"num", i+1,
			"total", len(pending),
			"name", transcript.Name,
		)

		// Classify: truncate content to 1000 chars for classification.
		preview := transcript.Content
		if len(preview) > 1000 {
			preview = preview[:1000]
		}
		meetingType, err := s.extractor.Classify(ctx, preview, s.templateKeys, s.cfg.Prompts.ClassifyPrompt)
		if err != nil {
			s.recordEvent(ctx, "classification_failed", "error", map[string]string{"transcript_id": transcript.ID, "error": err.Error()})
			slog.Error("classification failed", "id", transcript.ID, "error", err)
			continue
		}

		s.recordEvent(ctx, "classification_succeeded", "ok", map[string]string{"transcript_id": transcript.ID})

		// Normalize and cap the LLM classification result.
		const maxMeetingTypeLen = 64
		meetingType = strings.ToLower(strings.TrimSpace(meetingType))
		if len(meetingType) > maxMeetingTypeLen {
			slog.Error("meeting type from LLM exceeded max length, skipping",
				"id", transcript.ID, "len", len(meetingType))
			continue
		}

		// Look up template (fall back to first template if unknown key).
		tmpl, ok := s.cfg.Prompts.Templates[meetingType]
		if !ok {
			slog.Warn("unknown meeting type, using fallback", "type", meetingType, "fallback", s.templateKeys[0])
			tmpl = s.cfg.Prompts.Templates[s.templateKeys[0]]
		}

		// Extract: call with the template's extraction prompt.
		rawOutput, err := s.extractor.Extract(ctx, transcript.Content, tmpl.ExtractionPrompt)
		if err != nil {
			s.recordEvent(ctx, "extraction_failed", "error", map[string]string{"transcript_id": transcript.ID, "error": err.Error()})
			slog.Error("extraction failed", "id", transcript.ID, "error", err)
			if markErr := s.store.MarkFailed(ctx, transcript.ID, transcript.Name, "extraction failed: "+err.Error()); markErr != nil {
				slog.Error("mark failed failed", "id", transcript.ID, "error", markErr)
			}
			continue
		}

		s.recordEvent(ctx, "extraction_succeeded", "ok", map[string]string{"transcript_id": transcript.ID})

		// Parse: split front-matter + markdown.
		result, err := models.ParseExtractionOutput(rawOutput)
		if err != nil {
			slog.Error("parse extraction output failed", "id", transcript.ID, "error", err)
			if markErr := s.store.MarkFailed(ctx, transcript.ID, transcript.Name, "parse failed: "+err.Error()); markErr != nil {
				slog.Error("mark failed failed", "id", transcript.ID, "error", markErr)
			}
			continue
		}

		// Dry run check.
		if s.cfg.DryRun {
			preview := rawOutput
			if len(preview) > 200 {
				preview = preview[:200] + "... [truncated]"
			}
			slog.Info("dry run, skipping publish",
				"name", transcript.Name,
				"output_bytes", len(rawOutput),
				"output_preview", preview,
			)
			continue
		}

		// Publish.
		var confluenceURL string
		if !s.cfg.Confluence.IsEnabled() || s.publisher == nil {
			slog.Info("confluence disabled, skipping publish", "name", transcript.Name)
			skippedCount++
			continue
		}

		if s.publisher != nil {
			confluenceURL, err = s.publisher.PublishMarkdown(
				ctx,
				s.cfg.Confluence.SpaceKey,
				s.cfg.Confluence.ParentPageID,
				result,
				transcript.Name,
			)
			if err != nil {
				s.recordEvent(ctx, "publish_failed", "error", map[string]string{"transcript_id": transcript.ID, "error": err.Error()})
				publishFailures++
				slog.Error("publish failed", "id", transcript.ID, "error", err)
				if markErr := s.store.MarkFailed(ctx, transcript.ID, transcript.Name, "publish failed: "+err.Error()); markErr != nil {
					slog.Error("mark failed failed", "id", transcript.ID, "error", markErr)
				}
				continue
			}

			s.recordEvent(ctx, "publish_succeeded", "ok", map[string]string{"transcript_id": transcript.ID})
		}

		// Mark as processed in the database.
		if err := s.store.MarkProcessed(ctx, transcript.ID, transcript.Name, confluenceURL); err != nil {
			s.recordEvent(ctx, "state_persistence_failed", "error", map[string]string{"transcript_id": transcript.ID, "error": err.Error()})
			persistenceFailures++
			slog.Error("mark processed failed", "id", transcript.ID, "url", confluenceURL, "error", err)
			continue
		}

		s.recordEvent(ctx, "state_persistence_succeeded", "ok", map[string]string{"transcript_id": transcript.ID})

		slog.Info("published", "url", confluenceURL)
		successCount++
	}

	slog.Info("sync complete", "run_id", s.runID, "success", successCount, "total", len(pending), "persistence_failures", persistenceFailures, "publish_failures", publishFailures, "event_stage_counts", s.stageCounts)

	if persistenceFailures > 0 {
		return fmt.Errorf("%d transcript publishes succeeded but failed to persist state", persistenceFailures)
	}

	if successCount == 0 && len(pending) > 0 && !s.cfg.DryRun && skippedCount < len(pending) {
		return fmt.Errorf("all %d transcripts failed to process", len(pending))
	}

	s.recordEvent(ctx, "run_completed", "ok", map[string]string{"status": "completed"})

	return nil
}
