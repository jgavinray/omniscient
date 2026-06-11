package main

import (
	"fmt"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management commands",
	}

	configCmd.AddCommand(newConfigValidateCmd())

	return configCmd
}

func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("config validation failed: %w", err)
			}

			sources := ""
			if cfg.Sources.GoogleMeet.IsEnabled() {
				sources += "googlemeet "
			}
			destinations := ""
			if cfg.Destinations.Confluence.IsEnabled() {
				destinations += "confluence "
			}

			cmd.Printf("Configuration valid: %s\n", cfgFile)
			cmd.Printf("  Sources:      %s\n", sources)
			cmd.Printf("  Destinations: %s\n", destinations)
			cmd.Printf("  Provider:     %s\n", cfg.LLM.Provider)
			cmd.Printf("  Model:        %s\n", cfg.LLM.Model)
			cmd.Printf("  Lookback:     %d hours\n", cfg.Sync.LookbackHours)
			cmd.Printf("  Max/run:      %d\n", cfg.Sync.MaxPerRun)

			return nil
		},
	}
}
