package main

import (
	"fmt"
	"log/slog"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/source/googlemeet"
	"github.com/spf13/cobra"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth [provider]",
		Short: "Authenticate with a provider (run once; tokens auto-refresh afterwards)",
		// Bare `omniscient auth` keeps working while Google Meet is the only
		// OAuth provider.
		RunE: runGoogleMeetAuth,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "googlemeet",
		Short: "Authenticate with Google via OAuth2 browser consent flow",
		RunE:  runGoogleMeetAuth,
	})
	return cmd
}

func runGoogleMeetAuth(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	setupLogging(cfg.Logging.Level, cfg.Logging.File)

	slog.Info("starting Google OAuth2 authentication flow")

	_, err = googlemeet.RunAuthFlow(cfg.Sources.GoogleMeet.CredentialsFile, cfg.Sources.GoogleMeet.TokenFile)
	if err != nil {
		return fmt.Errorf("OAuth2 authentication failed: %w", err)
	}

	cmd.Println("Authentication successful! Token saved.")
	cmd.Printf("Token stored at: %s\n", cfg.Sources.GoogleMeet.TokenFile)

	return nil
}
