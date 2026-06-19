package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	cfgFile   string
	logLevel  string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "omniscient",
		Short: "Meeting transcript harvester: Google Drive → LLM extraction → Confluence",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			setupLogging(logLevel)
		},
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "/opt/omniscient/config.yaml", "config file path")

	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newSyncCmd())
	rootCmd.AddCommand(newAuthCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newRetryFailedCmd())
	rootCmd.AddCommand(newForgetCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("omniscient %s\n", version)
		},
	}
}

func setupLogging(level string) {
	var programLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		programLevel = slog.LevelDebug
	case "warn":
		programLevel = slog.LevelWarn
	case "error":
		programLevel = slog.LevelError
	default:
		programLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: programLevel}
	var w io.Writer = os.Stdout

	handler := slog.NewTextHandler(w, opts)
	slog.SetDefault(slog.New(handler))
}

func newStatusCmd() *cobra.Command {
	limit := 10
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show transcript pipeline status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			store, err := database.NewStore(cfg.Sync.DatabasePath)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer store.Close()

			ctx := cmd.Context()

			counts, err := store.StatusCounts(ctx)
			if err != nil {
				return fmt.Errorf("status counts: %w", err)
			}
			for _, status := range []string{
				database.StatusDiscovered,
				database.StatusExtracted,
				database.StatusFailed,
				database.StatusPublished,
				database.StatusSkipped,
			} {
				cmd.Printf("count\t%s\t%d\n", status, counts[status])
			}

			recent, err := store.RecentTranscripts(ctx, limit)
			if err != nil {
				return fmt.Errorf("recent transcripts: %w", err)
			}
			for _, rec := range recent {
				cmd.Printf("recent\t%s\t%s\t%d\t%s\t%s\n",
					rec.TranscriptID,
					rec.Status,
					rec.AttemptCount,
					rec.UpdatedAt.Format(time.RFC3339),
					rec.TranscriptName,
				)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "number of recent transcripts to show")
	return cmd
}

func newRetryFailedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retry-failed",
		Short: "Retry all failed transcripts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			store, err := database.NewStore(cfg.Sync.DatabasePath)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer store.Close()

			ctx := cmd.Context()

			n, err := store.RetryFailed(ctx)
			if err != nil {
				return fmt.Errorf("retry failed: %w", err)
			}
			cmd.Printf("retry-failed\t%d\n", n)
			return nil
		},
	}
	return cmd
}

func newForgetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forget <transcript-id>",
		Short: "Forget a transcript record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			store, err := database.NewStore(cfg.Sync.DatabasePath)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer store.Close()

			ctx := cmd.Context()

			n, err := store.ForgetTranscript(ctx, args[0])
			if err != nil {
				return fmt.Errorf("forget transcript: %w", err)
			}
			cmd.Printf("forget\t%s\t%d\n", args[0], n)
			return nil
		},
	}
	return cmd
}
