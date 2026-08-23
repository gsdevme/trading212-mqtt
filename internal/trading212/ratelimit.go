package trading212

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Limiter paces requests to one endpoint. It enforces a minimum spacing between
// calls and honours an explicit hold until a rate-limit reset time.
//
// It is a safety net rather than a throughput manager: at the default poll
// interval it never blocks. It exists because limits are enforced per account,
// so an over-eager or crash-looping process would throttle every other tool
// using the same credentials.
type Limiter struct {
	mu         sync.Mutex
	minSpacing time.Duration
	last       time.Time
	holdUntil  time.Time

	// now and sleep are injectable so tests run on a fake clock.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// NewLimiter returns a Limiter enforcing minSpacing between calls.
func NewLimiter(minSpacing time.Duration) *Limiter {
	return &Limiter{
		minSpacing: minSpacing,
		now:        time.Now,
		sleep:      sleepCtx,
	}
}

// Wait blocks until the next call is permitted, then records the call time.
func (l *Limiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := l.now()
		var delay time.Duration
		if l.holdUntil.After(now) {
			delay = l.holdUntil.Sub(now)
		} else if !l.last.IsZero() {
			if elapsed := now.Sub(l.last); elapsed < l.minSpacing {
				delay = l.minSpacing - elapsed
			}
		}
		if delay <= 0 {
			l.last = now
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()

		if err := l.sleep(ctx, delay); err != nil {
			return err
		}
	}
}

// HoldUntil suspends further calls until t. Used after a 429, where the reset
// timestamp is authoritative and guessing a backoff would be worse.
func (l *Limiter) HoldUntil(t time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.After(l.holdUntil) {
		l.holdUntil = t
	}
}

// Observe records the rate-limit headers from a response. When the remaining
// budget hits zero, calls are held until the advertised reset. Malformed or
// absent headers are ignored — they must never break a successful response.
func (l *Limiter) Observe(h http.Header) {
	remaining, err := strconv.Atoi(h.Get(headerRateLimitRemaining))
	if err != nil || remaining > 0 {
		return
	}
	reset, err := strconv.ParseInt(h.Get(headerRateLimitReset), 10, 64)
	if err != nil {
		return
	}
	l.HoldUntil(time.Unix(reset, 0))
}

// sleepCtx sleeps for d unless ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
