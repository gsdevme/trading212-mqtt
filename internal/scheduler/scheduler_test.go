package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsdevme/trading212-mqtt/internal/trading212"
)

type fakeFetcher struct {
	summaryCalls   int
	positionsCalls int
	summaryErr     error
	positionsErr   error
	// failuresBeforeSuccess makes the first N calls fail, exercising retry.
	failuresBeforeSuccess int
	gotCurrency           string
	// emptyCurrency makes AccountSummary return "" for Currency, exercising fetch's
	// fallback to Config.AccountCurrency.
	emptyCurrency bool
	// positions overrides the default single-holding response. Nil keeps it.
	positions []trading212.Position
}

func (f *fakeFetcher) AccountSummary(context.Context) (trading212.AccountSummary, error) {
	f.summaryCalls++
	if f.failuresBeforeSuccess > 0 {
		f.failuresBeforeSuccess--
		return trading212.AccountSummary{}, errors.New("transient")
	}
	if f.summaryErr != nil {
		return trading212.AccountSummary{}, f.summaryErr
	}
	if f.emptyCurrency {
		return trading212.AccountSummary{ID: 1, TotalValue: 100}, nil
	}
	return trading212.AccountSummary{ID: 1, Currency: "GBP", TotalValue: 100}, nil
}

func (f *fakeFetcher) Positions(_ context.Context, accountCurrency string) ([]trading212.Position, error) {
	f.positionsCalls++
	f.gotCurrency = accountCurrency
	if f.positionsErr != nil {
		return nil, f.positionsErr
	}
	if f.positions != nil {
		return f.positions, nil
	}
	return []trading212.Position{{Ticker: "AAPL_US_EQ"}}, nil
}

type fakePublisher struct {
	mu        sync.Mutex
	snapshots []trading212.Snapshot
	err       error
}

func (p *fakePublisher) PublishSnapshot(_ context.Context, s trading212.Snapshot) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.snapshots = append(p.snapshots, s)
	return nil
}

// snapshotCount reads the snapshot count under lock. TestRunPollsImmediatelyThenStopsOnContextCancel
// polls this from the test goroutine while Run's goroutine publishes concurrently.
func (p *fakePublisher) snapshotCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.snapshots)
}

type fakeHealth struct{ successes, failures int }

func (h *fakeHealth) MarkSuccess() { h.successes++ }
func (h *fakeHealth) MarkFailure() { h.failures++ }

func newTestScheduler(f Fetcher, p SnapshotPublisher, h HealthReporter) *Scheduler {
	return New(f, p, h, Config{
		AccountCurrency: "GBP",
		PollInterval:    time.Minute,
		MaxRetries:      0,
		// Instant "sleeps" keep retry tests fast and deterministic.
		After: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	})
}

func TestPollMakesExactlyTwoAPICalls(t *testing.T) {
	f, p, h := &fakeFetcher{}, &fakePublisher{}, &fakeHealth{}
	newTestScheduler(f, p, h).PollNow(context.Background())

	if f.summaryCalls != 1 || f.positionsCalls != 1 {
		t.Errorf("calls = (%d summary, %d positions), want (1, 1)", f.summaryCalls, f.positionsCalls)
	}
	if len(p.snapshots) != 1 {
		t.Fatalf("published %d snapshots, want 1", len(p.snapshots))
	}
	if h.successes != 1 || h.failures != 0 {
		t.Errorf("health = (%d success, %d failure), want (1, 0)", h.successes, h.failures)
	}
}

func TestPollBuildsSnapshotFromBothCalls(t *testing.T) {
	f, p, h := &fakeFetcher{}, &fakePublisher{}, &fakeHealth{}
	newTestScheduler(f, p, h).PollNow(context.Background())

	snap := p.snapshots[0]
	if snap.Account.TotalValue != 100 {
		t.Errorf("account not carried into the snapshot: %+v", snap.Account)
	}
	if len(snap.Positions) != 1 || snap.Positions[0].Ticker != "AAPL_US_EQ" {
		t.Errorf("positions not carried into the snapshot: %+v", snap.Positions)
	}
}

// The account currency from the summary response drives position parsing —
// it must win over the configured fallback, not merely match it. Config.AccountCurrency
// is deliberately set to a different value ("ZZZ") than the fake summary's "GBP" so
// this assertion can actually fail if fetch ever passes the config fallback instead
// of the summary's currency.
func TestPollPassesAccountCurrencyToPositions(t *testing.T) {
	f, p, h := &fakeFetcher{}, &fakePublisher{}, &fakeHealth{}
	s := New(f, p, h, Config{
		AccountCurrency: "ZZZ",
		PollInterval:    time.Minute,
		MaxRetries:      0,
		After: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	})
	s.PollNow(context.Background())

	if f.gotCurrency != "GBP" {
		t.Errorf("Positions received currency %q, want GBP from the summary response (not the ZZZ config fallback)", f.gotCurrency)
	}
}

// When the summary response omits a currency, fetch falls back to Config.AccountCurrency.
func TestPollFallsBackToConfiguredCurrencyWhenSummaryOmitsOne(t *testing.T) {
	f := &fakeFetcher{emptyCurrency: true}
	p, h := &fakePublisher{}, &fakeHealth{}
	s := New(f, p, h, Config{
		AccountCurrency: "ZZZ",
		PollInterval:    time.Minute,
		MaxRetries:      0,
		After: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	})
	s.PollNow(context.Background())

	if f.gotCurrency != "ZZZ" {
		t.Errorf("Positions received currency %q, want ZZZ config fallback when summary omits one", f.gotCurrency)
	}
}

func TestFailedFetchSkipsPublishAndMarksFailure(t *testing.T) {
	f := &fakeFetcher{summaryErr: errors.New("upstream down")}
	p, h := &fakePublisher{}, &fakeHealth{}
	newTestScheduler(f, p, h).PollNow(context.Background())

	if len(p.snapshots) != 0 {
		t.Error("a failed fetch must not publish — retained state stays at last known values")
	}
	if h.failures != 1 || h.successes != 0 {
		t.Errorf("health = (%d success, %d failure), want (0, 1)", h.successes, h.failures)
	}
}

func TestFailedPublishMarksFailure(t *testing.T) {
	f, h := &fakeFetcher{}, &fakeHealth{}
	p := &fakePublisher{err: errors.New("broker down")}
	newTestScheduler(f, p, h).PollNow(context.Background())

	if h.failures != 1 {
		t.Errorf("failures = %d, want 1", h.failures)
	}
}

func TestRetriesTransientFailures(t *testing.T) {
	f := &fakeFetcher{failuresBeforeSuccess: 2}
	p, h := &fakePublisher{}, &fakeHealth{}
	// backoffs records the duration passed to After on each retry. PollNow runs
	// synchronously (no goroutine), so a plain slice needs no locking here.
	var backoffs []time.Duration
	s := New(f, p, h, Config{
		AccountCurrency: "GBP",
		PollInterval:    time.Minute,
		MaxRetries:      3,
		After: func(d time.Duration) <-chan time.Time {
			backoffs = append(backoffs, d)
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	})
	s.PollNow(context.Background())

	if f.summaryCalls != 3 {
		t.Errorf("summaryCalls = %d, want 3 (two failures then a success)", f.summaryCalls)
	}
	if h.successes != 1 {
		t.Errorf("successes = %d, want 1", h.successes)
	}
	want := []time.Duration{time.Second, 2 * time.Second}
	if !slices.Equal(backoffs, want) {
		t.Errorf("backoffs = %v, want %v (exponential from 1s)", backoffs, want)
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	f := &fakeFetcher{summaryErr: errors.New("down")}
	p, h := &fakePublisher{}, &fakeHealth{}
	// backoffs records the duration passed to After on each retry. PollNow runs
	// synchronously (no goroutine), so a plain slice needs no locking here.
	var backoffs []time.Duration
	s := New(f, p, h, Config{
		AccountCurrency: "GBP",
		PollInterval:    time.Minute,
		MaxRetries:      2,
		After: func(d time.Duration) <-chan time.Time {
			backoffs = append(backoffs, d)
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	})
	s.PollNow(context.Background())

	if f.summaryCalls != 3 {
		t.Errorf("summaryCalls = %d, want 3 (initial attempt plus two retries)", f.summaryCalls)
	}
	if h.failures != 1 {
		t.Errorf("failures = %d, want 1", h.failures)
	}
	want := []time.Duration{time.Second, 2 * time.Second}
	if !slices.Equal(backoffs, want) {
		t.Errorf("backoffs = %v, want %v (exponential from 1s)", backoffs, want)
	}
}

func TestCancelledContextIsNotCountedAsFailure(t *testing.T) {
	f := &fakeFetcher{summaryErr: context.Canceled}
	p, h := &fakePublisher{}, &fakeHealth{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	newTestScheduler(f, p, h).PollNow(ctx)

	if h.failures != 0 {
		t.Errorf("failures = %d, want 0 — shutdown is not a fault", h.failures)
	}
}

func TestRunPollsImmediatelyThenStopsOnContextCancel(t *testing.T) {
	f, p, h := &fakeFetcher{}, &fakePublisher{}, &fakeHealth{}
	s := New(f, p, h, Config{AccountCurrency: "GBP", PollInterval: time.Hour})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	// The first poll is immediate, so a published snapshot proves Run started.
	deadline := time.After(2 * time.Second)
	for p.snapshotCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("no immediate first poll")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// New must normalise a non-positive PollInterval the same way it normalises
// Logger, After and MaxRetries: Run builds a time.Ticker from it, and
// time.NewTicker panics on a duration <= 0. The acceptance suite already
// constructs a Scheduler with PollInterval unset and escapes only because it
// calls PollNow rather than Run.
func TestNewClampsNonPositivePollInterval(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		s := New(&fakeFetcher{}, &fakePublisher{}, &fakeHealth{}, Config{PollInterval: d})
		if s.cfg.PollInterval != defaultPollInterval {
			t.Errorf("PollInterval %v was normalised to %v, want %v", d, s.cfg.PollInterval, defaultPollInterval)
		}

		// The behavioural half: Run must not panic, and must return on cancel.
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); s.Run(ctx) }()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after cancellation")
		}
	}
}

// newLoggingScheduler is newTestScheduler with the success line captured, so
// tests can assert on what an operator actually sees in kubectl logs.
func newLoggingScheduler(f Fetcher, p SnapshotPublisher, h HealthReporter, buf *bytes.Buffer) *Scheduler {
	return New(f, p, h, Config{
		AccountCurrency: "GBP",
		PollInterval:    time.Minute,
		MaxRetries:      0,
		Logger:          slog.New(slog.NewTextHandler(buf, nil)),
		After: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	})
}

// The held= attribute is the only place a default (empty TICKERS) deployment
// learns the ticker IDs the whitelist wants, and it is comma-joined so the value
// can be pasted into TICKERS verbatim.
func TestPollLogsHeldTickers(t *testing.T) {
	f := &fakeFetcher{positions: []trading212.Position{
		{Ticker: "VUSA_EQ"}, {Ticker: "AAPL_US_EQ"},
	}}
	var buf bytes.Buffer
	newLoggingScheduler(f, &fakePublisher{}, &fakeHealth{}, &buf).PollNow(context.Background())

	line := buf.String()
	if !strings.Contains(line, "published snapshot") {
		t.Fatalf("no success line logged: %q", line)
	}
	if !strings.Contains(line, "held=AAPL_US_EQ,VUSA_EQ") {
		t.Errorf("expected sorted, comma-joined tickers in held=; got %q", line)
	}
}

func TestPollLogsEmptyHeldForEmptyPortfolio(t *testing.T) {
	f := &fakeFetcher{positions: []trading212.Position{}}
	var buf bytes.Buffer
	newLoggingScheduler(f, &fakePublisher{}, &fakeHealth{}, &buf).PollNow(context.Background())

	if line := buf.String(); !strings.Contains(line, "held=") {
		t.Errorf("held= must still be logged for an empty portfolio; got %q", line)
	}
}
