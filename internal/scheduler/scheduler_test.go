package scheduler

import (
	"context"
	"errors"
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
	return trading212.AccountSummary{ID: 1, Currency: "GBP", TotalValue: 100}, nil
}

func (f *fakeFetcher) Positions(_ context.Context, accountCurrency string) ([]trading212.Position, error) {
	f.positionsCalls++
	f.gotCurrency = accountCurrency
	if f.positionsErr != nil {
		return nil, f.positionsErr
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

// The account currency from the summary response drives position parsing.
func TestPollPassesAccountCurrencyToPositions(t *testing.T) {
	f, p, h := &fakeFetcher{}, &fakePublisher{}, &fakeHealth{}
	newTestScheduler(f, p, h).PollNow(context.Background())

	if f.gotCurrency != "GBP" {
		t.Errorf("Positions received currency %q, want GBP from the summary response", f.gotCurrency)
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
	s := New(f, p, h, Config{
		AccountCurrency: "GBP",
		PollInterval:    time.Minute,
		MaxRetries:      3,
		After: func(time.Duration) <-chan time.Time {
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
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	f := &fakeFetcher{summaryErr: errors.New("down")}
	p, h := &fakePublisher{}, &fakeHealth{}
	s := New(f, p, h, Config{
		AccountCurrency: "GBP",
		PollInterval:    time.Minute,
		MaxRetries:      2,
		After: func(time.Duration) <-chan time.Time {
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
