package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/gsdevme/trading212-mqtt/internal/mock"
)

var mockAddr string

var mockCmd = &cobra.Command{
	Use:   "mock",
	Short: "Run a standalone mock Trading 212 API for local development",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runMock(cmd.Context(), mockAddr)
	},
}

func init() {
	mockCmd.Flags().StringVar(&mockAddr, "addr", mock.Address,
		"listen address; must match MOCK_URL used by serve")
}

func runMock(ctx context.Context, addr string) error {
	logger := newLogger("info", "text")
	srv := &http.Server{
		Addr:              addr,
		Handler:           mock.New(mock.Defaults()).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("mock API listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("mock shutdown: %w", err)
	}
	return nil
}
