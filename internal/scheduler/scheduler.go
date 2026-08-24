// Package scheduler runs the poll loop, turning two API calls per tick into one
// published snapshot and reporting the outcome to health. See
// docs/specs/04-polling-scheduling.md.
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/gsdevme/trading212-mqtt/internal/trading212"
)

// Fetcher reads the two endpoints one poll needs.
type Fetcher interface {
	AccountSummary(ctx context.Context) (trading212.AccountSummary, error)
	Positions(ctx context.Context, accountCurrency string) ([]trading212.Position, error)
}

// SnapshotPublisher publishes one poll's worth of state.
type SnapshotPublisher interface {
	PublishSnapshot(ctx context.Context, snap trading212.Snapshot) error
}

// HealthReporter records poll outcomes for readiness.
type HealthReporter interface {
	MarkSuccess()
	MarkFailure()
}

// defaultPollInterval is used when Config.PollInterval is non-positive. It
// matches config.POLL_INTERVAL's default; production always supplies a value
// above config.MinPollInterval, but that is an invariant of a different package
// and time.NewTicker panics on a non-positive duration, so New enforces a floor
// of its own rather than trusting its caller.
const defaultPollInterval = 5 * time.Minute

// Config configures the scheduler.
type Config struct {
	// AccountCurrency is the fallback currency for positions whose response
	// omits one. It is learned from the startup summary call.
	AccountCurrency string

	PollInterval time.Duration
	MaxRetries   int

	Logger *slog.Logger
	// After is injectable for deterministic tests.
	After func(time.Duration) <-chan time.Time
}

// Scheduler owns the poll loop.
type Scheduler struct {
	fetcher   Fetcher
	publisher SnapshotPublisher
	health    HealthReporter
	cfg       Config
	logger    *slog.Logger
	after     func(time.Duration) <-chan time.Time
}

// New builds a Scheduler.
func New(f Fetcher, p SnapshotPublisher, h HealthReporter, cfg Config) *Scheduler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	return &Scheduler{
		fetcher: f, publisher: p, health: h, cfg: cfg,
		logger: cfg.Logger, after: cfg.After,
	}
}

// Run polls immediately, then every PollInterval, and blocks until ctx is
// cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.poll(ctx) // immediate first poll: don't make Home Assistant wait an interval
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.poll(ctx)
		}
	}
}

// PollNow runs a single poll cycle. Exposed for the acceptance suite; the timed
// loop uses the same code path.
func (s *Scheduler) PollNow(ctx context.Context) { s.poll(ctx) }

// poll performs one fetch-and-publish cycle, updating health.
//
// A failed fetch deliberately skips the publish so retained state keeps its last
// known values rather than being overwritten with zeroes.
func (s *Scheduler) poll(ctx context.Context) {
	snap, err := s.fetchWithRetry(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return // shutdown, not a fault
		}
		s.logger.WarnContext(ctx, "poll failed", "err", err)
		s.health.MarkFailure()
		return
	}
	if err := s.publisher.PublishSnapshot(ctx, snap); err != nil {
		if ctx.Err() != nil {
			return
		}
		s.logger.WarnContext(ctx, "publish failed", "err", err)
		s.health.MarkFailure()
		return
	}
	s.logger.InfoContext(ctx, "published snapshot",
		"total_value", snap.Account.TotalValue,
		"positions", len(snap.Positions))
	s.health.MarkSuccess()
}

// fetchWithRetry performs both API calls, retrying transient errors with
// exponential backoff.
func (s *Scheduler) fetchWithRetry(ctx context.Context) (trading212.Snapshot, error) {
	var lastErr error
	backoff := time.Second
	for attempt := 0; attempt <= s.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return trading212.Snapshot{}, ctx.Err()
			case <-s.after(backoff):
			}
			backoff *= 2
		}

		snap, err := s.fetch(ctx)
		if err == nil {
			return snap, nil
		}
		if ctx.Err() != nil {
			return trading212.Snapshot{}, ctx.Err()
		}
		lastErr = err
		s.logger.DebugContext(ctx, "poll attempt failed", "attempt", attempt, "err", err)
	}
	return trading212.Snapshot{}, lastErr
}

// fetch performs the two API calls that make up one poll. The summary is fetched
// first because its currency governs how positions are interpreted.
func (s *Scheduler) fetch(ctx context.Context) (trading212.Snapshot, error) {
	account, err := s.fetcher.AccountSummary(ctx)
	if err != nil {
		return trading212.Snapshot{}, err
	}
	currency := account.Currency
	if currency == "" {
		currency = s.cfg.AccountCurrency
	}
	positions, err := s.fetcher.Positions(ctx, currency)
	if err != nil {
		return trading212.Snapshot{}, err
	}
	return trading212.Snapshot{Account: account, Positions: positions}, nil
}
