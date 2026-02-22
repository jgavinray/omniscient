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

			cmd.Printf("Configuration valid: %s\n", cfgFile)
			cmd.Printf("  Provider:  %s\n", cfg.LLM.Provider)
			cmd.Printf("  Model:     %s\n", cfg.LLM.Model)
			cmd.Printf("  Space:     %s\n", cfg.Confluence.SpaceKey)
			cmd.Printf("  Lookback:  %d hours\n", cfg.Sync.LookbackHours)
			cmd.Printf("  Max/run:   %d\n", cfg.Sync.MaxPerRun)

			return nil
		},
	}
}
