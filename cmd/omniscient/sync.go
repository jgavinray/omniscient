package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/jgavinray/omniscient/internal/destination"
	"github.com/jgavinray/omniscient/internal/destination/confluence"
	"github.com/jgavinray/omniscient/internal/llm"
	"github.com/jgavinray/omniscient/internal/pipeline"
	"github.com/jgavinray/omniscient/internal/source"
	"github.com/jgavinray/omniscient/internal/source/googlemeet"
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

// buildSources constructs every enabled source. To add a provider, append a
// block here — see docs/ADDING_A_PROVIDER.md.
func buildSources(ctx context.Context, cfg *config.Config) ([]source.Source, error) {
	var sources []source.Source
	if cfg.Sources.GoogleMeet.IsEnabled() {
		gm, err := googlemeet.New(ctx,
			cfg.Sources.GoogleMeet.CredentialsFile,
			cfg.Sources.GoogleMeet.TokenFile,
			cfg.Sources.GoogleMeet.FolderID,
		)
		if err != nil {
			return nil, fmt.Errorf("init googlemeet source: %w", err)
		}
		sources = append(sources, gm)
	}
	return sources, nil
}

// buildDestinations constructs every enabled destination. To add a provider,
// append a block here — see docs/ADDING_A_PROVIDER.md.
func buildDestinations(cfg *config.Config) []destination.Destination {
	var destinations []destination.Destination
	if cfg.Destinations.Confluence.IsEnabled() {
		destinations = append(destinations, confluence.NewPublisher(
			cfg.Destinations.Confluence.BaseURL,
			cfg.Destinations.Confluence.Email,
			cfg.Destinations.Confluence.APIToken,
			cfg.Destinations.Confluence.SpaceKey,
			cfg.Destinations.Confluence.ParentPageID,
		))
	}
	return destinations
}

// runSync wires up all dependencies and delegates to the pipeline service.
func runSync(ctx context.Context, cfg *config.Config) error {
	sources, err := buildSources(ctx, cfg)
	if err != nil {
		return err
	}

	llmExtractor, err := llm.NewExtractor(&cfg.LLM)
	if err != nil {
		return fmt.Errorf("init llm extractor: %w", err)
	}

	destinations := buildDestinations(cfg)

	store, err := database.NewStore(cfg.Sync.DatabasePath)
	if err != nil {
		return fmt.Errorf("init database: %w", err)
	}
	defer store.Close()

	// Adapt typed slices to the pipeline's local interfaces.
	pipeSources := make([]pipeline.Source, len(sources))
	for i, s := range sources {
		pipeSources[i] = s
	}
	pipeDestinations := make([]pipeline.Destination, len(destinations))
	for i, d := range destinations {
		pipeDestinations[i] = d
	}

	return pipeline.New(pipeSources, llmExtractor, pipeDestinations, store, cfg).Run(ctx)
}
