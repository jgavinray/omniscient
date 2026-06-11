// Package pipeline orchestrates the transcript flow: for every enabled
// source, fetch recent transcripts, classify the meeting type, extract
// structured notes via LLM, and publish to every enabled destination.
// Providers are plugged in via the source.Source and destination.Destination
// interfaces — see docs/ADDING_A_PROVIDER.md.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/jgavinray/omniscient/internal/models"
)

// Source fetches recent meeting transcripts from a meeting platform.
// It mirrors source.Source; declared locally so tests can fake it without
// importing provider packages.
type Source interface {
	Name() string
	ListRecent(ctx context.Context, since time.Duration) ([]*models.Transcript, error)
}

// Destination publishes extracted notes to a knowledge base (see
// destination.Destination for the idempotency contract).
type Destination interface {
	Name() string
	Publish(ctx context.Context, result *models.ExtractionResult, t *models.Transcript) (string, error)
}

// Extractor classifies meeting types and extracts structured notes via LLM.
type Extractor interface {
	Classify(ctx context.Context, transcriptPreview string, templateKeys []string, classifyPrompt string) (string, error)
	Extract(ctx context.Context, transcript string, extractionPrompt string) (string, error)
}

// StateStore tracks processed transcripts for idempotent pipeline runs.
// Keys are source-prefixed (models.Transcript.Key).
type StateStore interface {
	IsProcessed(ctx context.Context, key string) (bool, error)
	MarkProcessed(ctx context.Context, key, name, publishedURLs string) error
	MarkFailed(ctx context.Context, key, name, errorMessage string) error
	RecordSyncEvent(ctx context.Context, event *database.SyncEvent) error
}

// Service orchestrates the transcript processing pipeline.
type Service struct {
	sources      []Source
	extractor    Extractor
	destinations []Destination
	store        StateStore
	cfg          *config.Config
	templateKeys []string
	runID        string
	stageCounts  map[string]int
}

// New creates a Service with the given dependencies.
func New(sources []Source, extractor Extractor, destinations []Destination, store StateStore, cfg *config.Config) *Service {
	templateKeys := make([]string, 0, len(cfg.Prompts.Templates))
	for k := range cfg.Prompts.Templates {
		templateKeys = append(templateKeys, k)
	}
	sort.Strings(templateKeys)

	return &Service{
		sources:      sources,
		extractor:    extractor,
		destinations: destinations,
		store:        store,
		cfg:          cfg,
		templateKeys: templateKeys,
	}
}

// recordEvent appends a bounded metadata event for the given stage and status.
// On failure it logs a warning and continues — observability persistence
// failure must not break the pipeline.
func (s *Service) recordEvent(ctx context.Context, stage, status string, metadata map[string]string) {
	s.stageCounts[stage]++

	payload := map[string]string{"stage": stage, "status": status}
	for k, v := range database.TruncateMetadata(metadata) {
		payload[k] = v
	}
	metadataJSON, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("marshal sync event metadata failed", "run_id", s.runID, "stage", stage, "error", err)
		metadataJSON = []byte("{}")
	}

	event := &database.SyncEvent{
		ID:           fmt.Sprintf("evt-%s-%s", stage, time.Now().UTC().Format(time.RFC3339Nano)),
		RunID:        s.runID,
		TranscriptID: metadata["transcript_id"],
		Stage:        stage,
		Status:       status,
		MetadataJSON: string(metadataJSON),
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	if err := s.store.RecordSyncEvent(ctx, event); err != nil {
		slog.Warn("record sync event failed", "run_id", s.runID, "stage", stage, "error", err)
	}
}

// Run executes the full pipeline across all sources. A failing source is
// logged and skipped (recoverable); the run only errors when state
// persistence fails or every pending transcript fails.
func (s *Service) Run(ctx context.Context) error {
	s.runID = fmt.Sprintf("run-%s", time.Now().UTC().Format(time.RFC3339Nano))
	s.stageCounts = make(map[string]int)
	slog.Info("sync run started", "run_id", s.runID)
	s.recordEvent(ctx, "run_started", "ok", nil)

	lookback := time.Duration(s.cfg.Sync.LookbackHours) * time.Hour

	var pending []*models.Transcript
	for _, src := range s.sources {
		transcripts, err := src.ListRecent(ctx, lookback)
		if err != nil {
			s.recordEvent(ctx, "source_fetch_failed", "error", map[string]string{"source": src.Name(), "error": err.Error()})
			slog.Error("source fetch failed, skipping source", "source", src.Name(), "error", err)
			continue
		}
		s.recordEvent(ctx, "transcripts_fetched", "ok", map[string]string{"source": src.Name(), "count": fmt.Sprintf("%d", len(transcripts))})
		slog.Info("fetched transcripts", "run_id", s.runID, "source", src.Name(), "count", len(transcripts))

		for _, t := range transcripts {
			processed, err := s.store.IsProcessed(ctx, t.Key())
			if err != nil {
				slog.Warn("check processed failed", "key", t.Key(), "error", err)
				continue
			}
			if !processed {
				pending = append(pending, t)
				s.recordEvent(ctx, "transcript_discovered", "ok", map[string]string{"transcript_id": t.Key()})
			}
		}
	}

	slog.Info("pending transcripts", "count", len(pending))

	if len(pending) > s.cfg.Sync.MaxPerRun {
		slog.Warn("limiting transcripts", "pending", len(pending), "max", s.cfg.Sync.MaxPerRun)
		pending = pending[:s.cfg.Sync.MaxPerRun]
	}

	successCount := 0
	persistenceFailures := 0
	publishFailures := 0
	for i, t := range pending {
		if err := ctx.Err(); err != nil {
			slog.Warn("sync cancelled", "processed", successCount, "remaining", len(pending)-i)
			break
		}

		slog.Info("processing transcript", "num", i+1, "total", len(pending), "source", t.Source, "title", t.Title)

		if len(t.Content) > s.cfg.LLM.MaxTranscriptChars {
			slog.Warn("transcript exceeds max_transcript_chars; small models may truncate or degrade",
				"key", t.Key(), "chars", len(t.Content), "max", s.cfg.LLM.MaxTranscriptChars)
		}

		meetingType, err := s.classifyValidated(ctx, t)
		if err != nil {
			s.recordEvent(ctx, "classification_failed", "error", map[string]string{"transcript_id": t.Key(), "error": err.Error()})
			slog.Error("classification failed", "key", t.Key(), "error", err)
			continue
		}
		s.recordEvent(ctx, "classification_succeeded", "ok", map[string]string{"transcript_id": t.Key(), "type": meetingType})

		result, err := s.extractValidated(ctx, t, s.cfg.Prompts.Templates[meetingType].ExtractionPrompt)
		if err != nil {
			s.recordEvent(ctx, "extraction_failed", "error", map[string]string{"transcript_id": t.Key(), "error": err.Error()})
			slog.Error("extraction failed", "key", t.Key(), "error", err)
			s.markFailed(ctx, t, "extraction failed: "+err.Error())
			continue
		}
		s.recordEvent(ctx, "extraction_succeeded", "ok", map[string]string{"transcript_id": t.Key()})

		if s.cfg.DryRun {
			preview := result.Markdown
			if len(preview) > 200 {
				preview = preview[:200] + "... [truncated]"
			}
			slog.Info("dry run, skipping publish", "title", t.Title, "output_preview", preview)
			continue
		}

		urls, err := s.publishAll(ctx, result, t)
		if err != nil {
			publishFailures++
			s.markFailed(ctx, t, "publish failed: "+err.Error())
			continue
		}

		urlsJSON, err := json.Marshal(urls)
		if err != nil {
			urlsJSON = []byte("{}")
		}
		if err := s.store.MarkProcessed(ctx, t.Key(), t.Title, string(urlsJSON)); err != nil {
			s.recordEvent(ctx, "state_persistence_failed", "error", map[string]string{"transcript_id": t.Key(), "error": err.Error()})
			persistenceFailures++
			slog.Error("mark processed failed", "key", t.Key(), "error", err)
			continue
		}
		s.recordEvent(ctx, "state_persistence_succeeded", "ok", map[string]string{"transcript_id": t.Key()})

		slog.Info("published", "key", t.Key(), "urls", string(urlsJSON))
		successCount++
	}

	slog.Info("sync complete", "run_id", s.runID, "success", successCount, "total", len(pending),
		"persistence_failures", persistenceFailures, "publish_failures", publishFailures,
		"event_stage_counts", s.stageCounts)

	if persistenceFailures > 0 {
		return fmt.Errorf("%d transcript publishes succeeded but failed to persist state", persistenceFailures)
	}
	if successCount == 0 && len(pending) > 0 && !s.cfg.DryRun {
		return fmt.Errorf("all %d transcripts failed to process", len(pending))
	}

	s.recordEvent(ctx, "run_completed", "ok", nil)
	return nil
}

// classifyValidated asks the LLM for a meeting type and validates it against
// the configured templates. An invalid answer gets one corrective retry
// (small models often add prose around the key); if that also fails, it
// falls back to the first template key.
func (s *Service) classifyValidated(ctx context.Context, t *models.Transcript) (string, error) {
	preview := t.Content
	if len(preview) > 1000 {
		preview = preview[:1000]
	}

	raw, err := s.extractor.Classify(ctx, preview, s.templateKeys, s.cfg.Prompts.ClassifyPrompt)
	if err != nil {
		return "", err
	}
	if key, ok := s.normalizeType(raw); ok {
		return key, nil
	}

	corrective := s.cfg.Prompts.ClassifyPrompt + fmt.Sprintf(
		"\n\nYour previous answer %q was not a valid type. Respond with exactly one of: {{TEMPLATE_KEYS}} — a single word, nothing else.",
		truncateForPrompt(raw, 64))
	raw2, err := s.extractor.Classify(ctx, preview, s.templateKeys, corrective)
	if err != nil {
		return "", err
	}
	if key, ok := s.normalizeType(raw2); ok {
		return key, nil
	}

	fallback := s.templateKeys[0]
	slog.Warn("classification invalid after retry, using fallback template",
		"key", t.Key(), "answer", truncateForPrompt(raw2, 64), "fallback", fallback)
	return fallback, nil
}

// normalizeType lowercases/trims an LLM classification answer and reports
// whether it matches a configured template key.
func (s *Service) normalizeType(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	_, ok := s.cfg.Prompts.Templates[key]
	return key, ok
}

// extractValidated runs the extraction prompt and parses the output. On a
// parse failure it retries once, appending the parse error as corrective
// feedback — enough to recover most malformed answers from small models.
func (s *Service) extractValidated(ctx context.Context, t *models.Transcript, extractionPrompt string) (*models.ExtractionResult, error) {
	raw, err := s.extractor.Extract(ctx, t.Content, extractionPrompt)
	if err != nil {
		return nil, err
	}
	result, perr := models.ParseExtractionOutput(raw)
	if perr == nil {
		return result, nil
	}

	slog.Warn("extraction output malformed, retrying with feedback", "key", t.Key(), "error", perr)
	corrective := extractionPrompt + fmt.Sprintf(
		"\n\nIMPORTANT: your previous response could not be parsed (%s). Respond with YAML front-matter between --- delimiters followed by the markdown body. Start your response with --- on its own line.",
		perr.Error())
	raw2, err := s.extractor.Extract(ctx, t.Content, corrective)
	if err != nil {
		return nil, err
	}
	result2, perr2 := models.ParseExtractionOutput(raw2)
	if perr2 != nil {
		return nil, fmt.Errorf("extraction output invalid after retry: %w", perr2)
	}
	return result2, nil
}

// publishAll publishes to every destination, collecting name → URL. It stops
// at the first failure; destinations are idempotent, so the whole transcript
// is retried next run without creating duplicates.
func (s *Service) publishAll(ctx context.Context, result *models.ExtractionResult, t *models.Transcript) (map[string]string, error) {
	urls := make(map[string]string, len(s.destinations))
	for _, dest := range s.destinations {
		u, err := dest.Publish(ctx, result, t)
		if err != nil {
			s.recordEvent(ctx, "publish_failed", "error", map[string]string{"transcript_id": t.Key(), "destination": dest.Name(), "error": err.Error()})
			slog.Error("publish failed", "key", t.Key(), "destination", dest.Name(), "error", err)
			return nil, fmt.Errorf("destination %s: %w", dest.Name(), err)
		}
		urls[dest.Name()] = u
		s.recordEvent(ctx, "publish_succeeded", "ok", map[string]string{"transcript_id": t.Key(), "destination": dest.Name()})
	}
	return urls, nil
}

// markFailed records a failure, logging (not returning) persistence errors.
func (s *Service) markFailed(ctx context.Context, t *models.Transcript, msg string) {
	if err := s.store.MarkFailed(ctx, t.Key(), t.Title, msg); err != nil {
		slog.Error("mark failed failed", "key", t.Key(), "error", err)
	}
}

// truncateForPrompt bounds untrusted LLM output before echoing it back.
func truncateForPrompt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
