package main

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	version  = "dev"
	cfgFile  string
	logLevel string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "omniscient",
		Short: "Meeting transcript harvester: Google Drive → LLM extraction → Confluence",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			setupLogging(logLevel, "")
		},
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "/opt/omniscient/config.yaml", "config file path")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")

	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newSyncCmd())
	rootCmd.AddCommand(newAuthCmd())
	rootCmd.AddCommand(newConfigCmd())

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

func setupLogging(level, filePath string) {
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
	if filePath != "" {
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			// Fall back to stdout if we can't open the log file.
			slog.Error("could not open log file, falling back to stdout", "path", filePath, "error", err)
		} else {
			w = io.MultiWriter(os.Stdout, f)
		}
	}

	handler := slog.NewTextHandler(w, opts)
	slog.SetDefault(slog.New(handler))
}
