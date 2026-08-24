package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/gsdevme/trading212-mqtt/internal/config"
	"github.com/gsdevme/trading212-mqtt/internal/trading212"
)

var dumpCmd = &cobra.Command{
	Use:    "dump <path>",
	Short:  "Print a raw API response to stdout (diagnostic)",
	Long:   "Fetch one API path through the normal auth and pacing path and write the raw JSON to stdout. Nothing is written to disk.",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDump(cmd.Context(), args[0])
	},
}

func runDump(ctx context.Context, path string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	client, err := trading212.New(trading212.Config{
		BaseURL:   cfg.APIBaseURL,
		APIKey:    cfg.APIKey,
		APISecret: cfg.APISecret,
		Logger:    newLogger(cfg.LogLevel, "text"),
	})
	if err != nil {
		return err
	}

	body, err := client.Get(ctx, path)
	if err != nil {
		return err
	}
	// stdout only — this service never writes to disk.
	if _, err := os.Stdout.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}
