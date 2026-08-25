package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/confluence"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/jgavinray/omniscient/internal/drive"
	"github.com/jgavinray/omniscient/internal/llm"
	"github.com/jgavinray/omniscient/internal/models"
	"github.com/jgavinray/omniscient/internal/publish"
	"github.com/spf13/cobra"
)

// runOptions carries execution-time options for the sync pipeline.
type runOptions struct {
	interactive bool
	stdin       io.Reader
	stdout      io.Writer
}

func newSyncCmd() *cobra.Command {
	var interactive bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch, extract, and route recent meeting transcripts to configured sinks",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			setupLogging(cfg.Logging.Level, cfg.Logging.File)

			// Set up context with signal handling for graceful shutdown.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return runSync(ctx, cfg, &runOptions{
				interactive: interactive,
				stdin:       cmd.InOrStdin(),
				stdout:      cmd.OutOrStdout(),
			})
		},
	}
	cmd.Flags().BoolVar(&interactive, "interactive", false,
		"show each extracted summary in the terminal and prompt for approval / sink selection before publishing")
	return cmd
}

// runSync wires up all dependencies and delegates to the sync service.
func runSync(ctx context.Context, cfg *config.Config, opts *runOptions) error {
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

	// Build the list of enabled sinks.
	var sinks []publish.Sink
	if cfg.Confluence.IsEnabled() {
		client := confluence.NewClient(
			cfg.Confluence.BaseURL,
			cfg.Confluence.Email,
			cfg.Confluence.APIToken,
		)
		sinks = append(sinks, publish.NewConfluenceSink(client, cfg.Confluence.SpaceKey, cfg.Confluence.ParentPageID))
	}
	if cfg.Slack.IsEnabled() {
		sinks = append(sinks, publish.NewSlackSink(cfg.Slack.WebhookURL))
	}
	if cfg.Local.IsEnabled() {
		sinks = append(sinks, publish.NewLocalSink(cfg.Local.OutputDir))
	}

	if opts.interactive && len(sinks) == 0 {
		return fmt.Errorf("interactive mode requires at least one enabled sink")
	}
	if !cfg.DryRun && len(sinks) == 0 {
		return fmt.Errorf("no sinks enabled: enable confluence, slack, or local in config.yaml")
	}

	sinkNames := make([]string, len(sinks))
	for i, s := range sinks {
		sinkNames[i] = s.Name()
	}
	slog.Info("sinks enabled", "sinks", sinkNames)

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
		sinks,
		store,
		cfg,
		opts,
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

// stateStore tracks processed transcripts for idempotent pipeline runs.
type stateStore interface {
	IsProcessed(ctx context.Context, transcriptID string) (bool, error)
	MarkProcessed(ctx context.Context, transcriptID, transcriptName, sinkResults string) error
	MarkFailed(ctx context.Context, transcriptID, transcriptName, errorMessage string) error
	RecordSyncEvent(ctx context.Context, event *database.SyncEvent) error
}

// SyncService orchestrates the transcript processing pipeline.
type SyncService struct {
	fetcher      fetcher
	extractor    extractor
	sinks        []publish.Sink
	sinkNames    []string
	store        stateStore
	cfg          *config.Config
	opts         *runOptions
	templateKeys []string
	runID        string
	stageCounts  map[string]int
}

// NewSyncService creates a SyncService with the given dependencies.
func NewSyncService(
	fetcher fetcher,
	extractor extractor,
	sinks []publish.Sink,
	store stateStore,
	cfg *config.Config,
	opts *runOptions,
) *SyncService {
	// Pre-compute sorted template keys.
	templateKeys := make([]string, 0, len(cfg.Prompts.Templates))
	for k := range cfg.Prompts.Templates {
		templateKeys = append(templateKeys, k)
	}
	sort.Strings(templateKeys)

	sinkNames := make([]string, len(sinks))
	for i, s := range sinks {
		sinkNames[i] = s.Name()
	}

	return &SyncService{
		fetcher:      fetcher,
		extractor:    extractor,
		sinks:        sinks,
		sinkNames:    sinkNames,
		store:        store,
		cfg:          cfg,
		opts:         opts,
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
// route → mark.  It returns early on context cancellation.
func (s *SyncService) Run(ctx context.Context) error {
	// Refuse to run without any enabled sink (unless dry-run): marking
	// transcripts processed without publishing anywhere would silently lose
	// them. runSync enforces the same guard earlier, this is a defense in depth.
	if len(s.sinks) == 0 && !s.cfg.DryRun {
		return fmt.Errorf("no sinks enabled: enable confluence, slack, or local in config.yaml")
	}
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

	// Process each transcript: classify → extract → parse → route → mark.
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

		// Classify: truncate content to 1000 chars (UTF-8 safe) for classification.
		preview := truncateRuneSafe(transcript.Content, 1000)
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
			fmt.Fprintf(s.opts.stdout, "--- DRY RUN: %s ---\n%s\n", transcript.Name, rawOutput)
			continue
		}

		// Decide which sinks to route this transcript to.
		targets := s.sinks
		if s.opts.interactive {
			fmt.Fprintf(s.opts.stdout, "\n================  %s  ================\n%s\n", transcript.Name, rawOutput)
			choice, skip, err := promptForSinks(s.opts.stdin, s.opts.stdout, s.sinkNames)
			if err != nil {
				slog.Error("interactive prompt failed", "id", transcript.ID, "error", err)
				continue
			}
			if skip {
				// Mark processed (with empty results) so a user-skipped
				// transcript is not re-prompted on the next run.
				if err := s.store.MarkProcessed(ctx, transcript.ID, transcript.Name, "[]"); err != nil {
					slog.Error("mark skipped failed", "id", transcript.ID, "error", err)
					continue
				}
				skippedCount++
				slog.Info("transcript skipped by user", "id", transcript.ID)
				continue
			}
			targets = make([]publish.Sink, 0, len(choice))
			for _, idx := range choice {
				targets = append(targets, s.sinks[idx])
			}
		}

		// Publish to each target sink. All-or-nothing: if any sink fails the
		// transcript is NOT marked processed (it is marked failed), so it is
		// retried on the next run. Sinks are idempotent (Confluence updates by
		// title, local files overwrite); Slack is the only sink that can
		// duplicate on retry.
		results := make([]publish.Result, 0, len(targets))
		failed := false
		for _, sink := range targets {
			ref, err := sink.Publish(ctx, result, transcript.Name)
			if err != nil {
				s.recordEvent(ctx, "publish_failed", "error", map[string]string{"transcript_id": transcript.ID, "sink": sink.Name(), "error": err.Error()})
				publishFailures++
				slog.Error("sink publish failed", "id", transcript.ID, "sink", sink.Name(), "error", err)
				if markErr := s.store.MarkFailed(ctx, transcript.ID, transcript.Name, "publish failed to "+sink.Name()+": "+err.Error()); markErr != nil {
					slog.Error("mark failed failed", "id", transcript.ID, "error", markErr)
				}
				failed = true
				break
			}
			results = append(results, publish.Result{Sink: sink.Name(), Ref: ref})
			s.recordEvent(ctx, "publish_succeeded", "ok", map[string]string{"transcript_id": transcript.ID, "sink": sink.Name()})
		}
		if failed {
			continue
		}

		resultsJSON, err := publish.MarshalResults(results)
		if err != nil {
			slog.Error("marshal sink results failed", "id", transcript.ID, "error", err)
			continue
		}

		// Mark as processed in the database with the serialized sink results.
		if err := s.store.MarkProcessed(ctx, transcript.ID, transcript.Name, resultsJSON); err != nil {
			s.recordEvent(ctx, "state_persistence_failed", "error", map[string]string{"transcript_id": transcript.ID, "error": err.Error()})
			persistenceFailures++
			slog.Error("mark processed failed", "id", transcript.ID, "results", resultsJSON, "error", err)
			continue
		}

		s.recordEvent(ctx, "state_persistence_succeeded", "ok", map[string]string{"transcript_id": transcript.ID})

		slog.Info("routed transcript", "id", transcript.ID, "results", resultsJSON)
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

// promptForSinks displays the available sinks and reads a single line of
// input:
//   - "a", "all", or empty → select all sinks
//   - "n" / "skip"         → skip this transcript entirely
//   - comma-separated 1-based indices (e.g. "1,3") → select those sinks
//
// It returns the selected sink indices and whether the transcript was skipped.
func promptForSinks(in io.Reader, out io.Writer, names []string) ([]int, bool, error) {
	w := bufio.NewWriter(out)
	for i, n := range names {
		fmt.Fprintf(w, "  [%d] %s\n", i+1, n)
	}
	fmt.Fprint(w, "Route to sink(s)? [a=all, n=skip, or numbers e.g. 1,3]: ")
	if err := w.Flush(); err != nil {
		return nil, false, fmt.Errorf("write prompt: %w", err)
	}

	br := bufio.NewReader(in)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, false, fmt.Errorf("reading input: %w", err)
	}
	line = strings.ToLower(strings.TrimSpace(line))

	switch line {
	case "", "a", "all":
		choice := make([]int, len(names))
		for i := range names {
			choice[i] = i
		}
		return choice, false, nil
	case "n", "skip":
		return nil, true, nil
	}

	var choice []int
	for _, part := range strings.Split(line, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx, perr := strconv.Atoi(part)
		if perr != nil {
			return nil, false, fmt.Errorf("invalid selection %q (use a, n, or comma-separated sink numbers)", line)
		}
		if idx < 1 || idx > len(names) {
			return nil, false, fmt.Errorf("sink index %d out of range (1-%d)", idx, len(names))
		}
		choice = append(choice, idx-1)
	}
	if len(choice) == 0 {
		return nil, false, fmt.Errorf("no valid sinks selected")
	}
	return choice, false, nil
}

// truncateRuneSafe truncates s to at most max bytes without splitting a
// multi-byte UTF-8 rune.
func truncateRuneSafe(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}
