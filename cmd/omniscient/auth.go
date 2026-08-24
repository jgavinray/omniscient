package main

import (
	"fmt"
	"log/slog"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/drive"
	"github.com/spf13/cobra"
)

func newAuthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with Google Drive via OAuth2 browser consent flow",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			setupLogging(cfg.Logging.Level, cfg.Logging.File)

			slog.Info("starting Google OAuth2 authentication flow")

			_, err = drive.RunAuthFlow(cfg.Google.CredentialsFile, cfg.Google.TokenFile)
			if err != nil {
				return fmt.Errorf("OAuth2 authentication failed: %w", err)
			}

			cmd.Println("Authentication successful! Token saved.")
			cmd.Printf("Token stored at: %s\n", cfg.Google.TokenFile)

			return nil
		},
	}
}
