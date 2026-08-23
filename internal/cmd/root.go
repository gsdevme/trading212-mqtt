// Package cmd wires the CLI: `serve` runs the poll->MQTT service, `mock` runs the
// standalone fake API, `dump` prints a raw API response for diagnosis.
package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "trading212-mqtt",
	Short: "Publish Trading 212 account and position metrics to MQTT with Home Assistant autodiscovery",
	// Report errors and exit codes from main() instead: don't let cobra print the
	// error or dump usage on runtime failures.
	SilenceErrors: true,
	SilenceUsage:  true,
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		// Load a local .env if present (a no-op in production).
		_ = godotenv.Load()
	},
}

func init() {
	rootCmd.AddCommand(mockCmd)
}

// Execute runs the root command, returning any error for main to report and map
// to a non-zero exit code. Signal handling is installed once here so every
// subcommand receives a context cancelled on SIGTERM/SIGINT via cmd.Context().
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

// newLogger builds a slog logger from level/format strings.
func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
