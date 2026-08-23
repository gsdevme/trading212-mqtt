package trading212

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// fakeClock drives the limiter deterministically: sleeping advances now instead
// of blocking, so the tests take no wall-clock time.
type fakeClock struct {
	now      time.Time
	slept    []time.Duration
	sleepErr error
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	if c.sleepErr != nil {
		return c.sleepErr
	}
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	return nil
}

func newTestLimiter(spacing time.Duration) (*Limiter, *fakeClock) {
	clk := &fakeClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	l := NewLimiter(spacing)
	l.now = clk.Now
	l.sleep = clk.Sleep
	return l, clk
}

func TestLimiterFirstCallDoesNotWait(t *testing.T) {
	l, clk := newTestLimiter(5 * time.Second)
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(clk.slept) != 0 {
		t.Errorf("slept %v on the first call, want none", clk.slept)
	}
}

func TestLimiterEnforcesSpacing(t *testing.T) {
	l, clk := newTestLimiter(5 * time.Second)
	ctx := context.Background()

	if err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// Immediately again: must wait the full spacing.
	if err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(clk.slept) != 1 || clk.slept[0] != 5*time.Second {
		t.Fatalf("slept = %v, want one 5s wait", clk.slept)
	}
}

func TestLimiterDoesNotWaitWhenSpacingAlreadyElapsed(t *testing.T) {
	l, clk := newTestLimiter(5 * time.Second)
	ctx := context.Background()

	if err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	clk.now = clk.now.Add(10 * time.Second)
	if err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(clk.slept) != 0 {
		t.Errorf("slept %v, want none once the spacing had elapsed", clk.slept)
	}
}

func TestLimiterHoldUntilBlocksUntilReset(t *testing.T) {
	l, clk := newTestLimiter(time.Second)
	ctx := context.Background()

	resetAt := clk.now.Add(30 * time.Second)
	l.HoldUntil(resetAt)

	if err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(clk.slept) != 1 || clk.slept[0] != 30*time.Second {
		t.Fatalf("slept = %v, want a single 30s hold", clk.slept)
	}
	if clk.now.Before(resetAt) {
		t.Errorf("now = %v, want at least %v", clk.now, resetAt)
	}
}

func TestLimiterObserveHoldsWhenBudgetExhausted(t *testing.T) {
	l, clk := newTestLimiter(time.Second)
	resetAt := clk.now.Add(42 * time.Second)

	h := http.Header{}
	h.Set(headerRateLimitRemaining, "0")
	h.Set(headerRateLimitReset, strconv.FormatInt(resetAt.Unix(), 10))
	l.Observe(h)

	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(clk.slept) != 1 {
		t.Fatalf("slept = %v, want one hold until reset", clk.slept)
	}
}

func TestLimiterObserveIgnoresHealthyBudget(t *testing.T) {
	l, clk := newTestLimiter(time.Second)

	h := http.Header{}
	h.Set(headerRateLimitRemaining, "17")
	h.Set(headerRateLimitReset, strconv.FormatInt(clk.now.Add(time.Minute).Unix(), 10))
	l.Observe(h)

	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(clk.slept) != 0 {
		t.Errorf("slept %v, want none while budget remains", clk.slept)
	}
}

func TestLimiterObserveToleratesMissingHeaders(t *testing.T) {
	l, _ := newTestLimiter(time.Second)
	l.Observe(http.Header{}) // must not panic
	l.Observe(http.Header{headerRateLimitRemaining: []string{"nonsense"}})
}

func TestLimiterWaitRespectsContext(t *testing.T) {
	l, clk := newTestLimiter(5 * time.Second)
	ctx := context.Background()
	if err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	clk.sleepErr = context.Canceled
	if err := l.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}
}
