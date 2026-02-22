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

	// Fetch recent transcripts from Google Drive.
	lookback := time.Duration(cfg.Sync.LookbackHours) * time.Hour
	transcripts, err := driveClient.GetRecentTranscripts(ctx, cfg.Google.FolderID, lookback)
	if err != nil {
		return fmt.Errorf("fetch transcripts: %w", err)
	}

	slog.Info("fetched transcripts", "count", len(transcripts))

	// Filter out already-processed transcripts.
	var pending []*drive.Transcript
	for _, t := range transcripts {
		processed, err := store.IsProcessed(ctx, t.ID)
		if err != nil {
			slog.Warn("check processed failed", "id", t.ID, "error", err)
			continue
		}
		if !processed {
			pending = append(pending, t)
		}
	}

	slog.Info("pending transcripts", "count", len(pending))

	// Limit to max_per_run to prevent runaway processing.
	if len(pending) > cfg.Sync.MaxPerRun {
		slog.Warn("limiting transcripts", "pending", len(pending), "max", cfg.Sync.MaxPerRun)
		pending = pending[:cfg.Sync.MaxPerRun]
	}

	// Build sorted template key list for classification.
	templateKeys := make([]string, 0, len(cfg.Prompts.Templates))
	for k := range cfg.Prompts.Templates {
		templateKeys = append(templateKeys, k)
	}
	sort.Strings(templateKeys)

	// Process each transcript: classify → extract → parse → publish → mark.
	successCount := 0
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
		meetingType, err := llmExtractor.Classify(ctx, preview, templateKeys, cfg.Prompts.ClassifyPrompt)
		if err != nil {
			slog.Error("classification failed", "id", transcript.ID, "error", err)
			continue
		}

		// Normalize and cap the LLM classification result.
		const maxMeetingTypeLen = 64
		meetingType = strings.ToLower(strings.TrimSpace(meetingType))
		if len(meetingType) > maxMeetingTypeLen {
			slog.Error("meeting type from LLM exceeded max length, skipping",
				"id", transcript.ID, "len", len(meetingType))
			continue
		}

		// Look up template (fall back to first template if unknown key).
		tmpl, ok := cfg.Prompts.Templates[meetingType]
		if !ok {
			slog.Warn("unknown meeting type, using fallback", "type", meetingType, "fallback", templateKeys[0])
			tmpl = cfg.Prompts.Templates[templateKeys[0]]
		}

		// Extract: call with the template's extraction prompt.
		rawOutput, err := llmExtractor.Extract(ctx, transcript.Content, tmpl.ExtractionPrompt)
		if err != nil {
			slog.Error("extraction failed", "id", transcript.ID, "error", err)
			continue
		}

		// Parse: split front-matter + markdown.
		result, err := models.ParseExtractionOutput(rawOutput)
		if err != nil {
			slog.Error("parse extraction output failed", "id", transcript.ID, "error", err)
			continue
		}

		// Dry run check.
		if cfg.DryRun {
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
		if confluenceClient != nil {
			confluenceURL, err = confluenceClient.PublishMarkdown(
				ctx,
				cfg.Confluence.SpaceKey,
				cfg.Confluence.ParentPageID,
				result,
				transcript.Name,
			)
			if err != nil {
				slog.Error("publish failed", "id", transcript.ID, "error", err)
				continue
			}
		} else {
			slog.Info("confluence disabled, skipping publish", "name", transcript.Name)
			confluenceURL = "confluence-disabled"
		}

		// Mark as processed in the database.
		if err := store.MarkProcessed(ctx, transcript.ID, transcript.Name, confluenceURL); err != nil {
			slog.Error("mark processed failed", "id", transcript.ID, "error", err)
		}

		slog.Info("published", "url", confluenceURL)
		successCount++
	}

	slog.Info("sync complete", "success", successCount, "total", len(pending))

	if successCount == 0 && len(pending) > 0 && !cfg.DryRun {
		return fmt.Errorf("all %d transcripts failed to process", len(pending))
	}

	return nil
}