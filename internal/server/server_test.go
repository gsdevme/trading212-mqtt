package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestServer() *Server {
	return New(Config{ReadyFailureThreshold: 3, PollInterval: 5 * time.Minute, Mode: "mock"})
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

func TestHealthzIsAlwaysOK(t *testing.T) {
	s := newTestServer()
	if rr := get(t, s, "/healthz"); rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 before any poll", rr.Code)
	}
	s.MarkFailure()
	s.MarkFailure()
	s.MarkFailure()
	if rr := get(t, s, "/healthz"); rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 even when not ready", rr.Code)
	}
}

func TestReadyzStartsNotReady(t *testing.T) {
	if rr := get(t, newTestServer(), "/readyz"); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 before the first successful publish", rr.Code)
	}
}

func TestReadyzAfterFirstSuccess(t *testing.T) {
	s := newTestServer()
	s.MarkSuccess()
	if rr := get(t, s, "/readyz"); rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestReadyzFlipsAfterThresholdFailures(t *testing.T) {
	s := newTestServer()
	s.MarkSuccess()

	s.MarkFailure()
	s.MarkFailure()
	if !s.Ready() {
		t.Fatal("must stay ready below the threshold")
	}
	s.MarkFailure()
	if s.Ready() {
		t.Error("must go not-ready at the threshold")
	}
}

func TestSuccessClearsTheFailureCounter(t *testing.T) {
	s := newTestServer()
	s.MarkSuccess()
	s.MarkFailure()
	s.MarkFailure()
	s.MarkSuccess()
	s.MarkFailure()
	s.MarkFailure()
	if !s.Ready() {
		t.Error("a success must reset the consecutive-failure counter")
	}
}

func TestThresholdBelowOneIsTreatedAsOne(t *testing.T) {
	s := New(Config{ReadyFailureThreshold: 0})
	s.MarkSuccess()
	s.MarkFailure()
	if s.Ready() {
		t.Error("a zero threshold must behave as 1")
	}
}

// REQ-LC-06: the account id is personal, so only its last four digits appear.
func TestStatusPageMasksAccountID(t *testing.T) {
	s := newTestServer()
	s.SetAccount(12345678, "GBP")

	rr := get(t, s, "/")
	body := rr.Body.String()
	if strings.Contains(body, "12345678") {
		t.Error("the full account id must never appear on the status page")
	}
	if !strings.Contains(body, "5678") {
		t.Errorf("expected the masked id to show the last four digits; got %q", body)
	}
}

func TestStatusPageShowsMetrics(t *testing.T) {
	s := newTestServer()
	s.SetAccount(12345678, "GBP")
	pct := 11.2
	s.SetMetrics(Metrics{
		TotalValue: 15234.56, FreeCash: 1200, Invested: 12500,
		UnrealizedPL: 1400, ReturnPct: &pct, Currency: "GBP",
		PositionCount: 3, TrackedCount: 1, LastUpdated: time.Now(),
	})

	body := get(t, s, "/").Body.String()
	for _, want := range []string{
		" <tr><th>Total value</th><td>15234.56 GBP</td></tr>",
		" <tr><th>Free cash</th><td>1200.00 GBP</td></tr>",
		" <tr><th>Positions</th><td>1 tracked of 3 held</td></tr>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("status page missing %q", want)
		}
	}
}

func TestStatusPageBeforeAnyPoll(t *testing.T) {
	rr := get(t, newTestServer(), "/")
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 during init", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "initialising") {
		t.Error("expected an initialising state before the first poll")
	}
}

func TestUnknownPathIs404(t *testing.T) {
	if rr := get(t, newTestServer(), "/nope"); rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// TestSetMetricsDoesNotAliasReturnPct guards against SetMetrics storing the
// caller's *float64 as-is: a caller that reuses one backing variable across
// polls (e.g. a pct hoisted out of a loop) would otherwise race with
// handleRoot dereferencing that pointer after the lock is released. Runs fast
// and deterministically fails under -race without the defensive copy in
// SetMetrics, so it stays in the suite.
func TestSetMetricsDoesNotAliasReturnPct(t *testing.T) {
	s := newTestServer()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				get(t, s, "/")
			}
		}
	}()

	// The caller reuses ONE backing variable across calls.
	var pct float64
	for i := 0; i < 2000; i++ {
		pct = float64(i)
		s.SetMetrics(Metrics{ReturnPct: &pct})
	}

	close(stop)
	wg.Wait()
}

// The holdings table is the answer to "which ticker IDs does TICKERS want?", so
// it must show every held position — tracked or not — and offer the list in a
// form that pastes straight into TICKERS.
func TestStatusPageShowsHoldings(t *testing.T) {
	s := newTestServer()
	s.SetAccount(12345678, "GBP")
	s.SetMetrics(Metrics{
		Currency: "GBP", PositionCount: 2, TrackedCount: 1, LastUpdated: time.Now(),
		Positions: []Position{
			{Ticker: "AAPL_US_EQ", Name: "Apple Inc", Value: 1234.5, Tracked: true},
			{Ticker: "FREE_EQ", Name: "Freetrade", Value: 67.89, Tracked: false},
		},
	})

	body := get(t, s, "/").Body.String()
	for _, want := range []string{
		"<td>AAPL_US_EQ</td><td>Apple Inc</td><td class=\"num\">1234.50 GBP</td><td>yes</td>",
		"<td>FREE_EQ</td><td>Freetrade</td><td class=\"num\">67.89 GBP</td><td>no</td>",
		"<code>TICKERS=AAPL_US_EQ,FREE_EQ</code>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("status page missing %q", want)
		}
	}
}

func TestStatusPageOmitsHoldingsTableWhenEmpty(t *testing.T) {
	s := newTestServer()
	s.SetMetrics(Metrics{Currency: "GBP"})

	if body := get(t, s, "/").Body.String(); strings.Contains(body, "TICKERS=") {
		t.Error("the TICKERS line must not render for an account with no holdings")
	}
}

// TestSetMetricsDoesNotAliasPositions is TestSetMetricsDoesNotAliasReturnPct for
// the slice: handleRoot ranges over Metrics.Positions after releasing the lock,
// so a caller that reuses one backing array across polls would otherwise race.
func TestSetMetricsDoesNotAliasPositions(t *testing.T) {
	s := newTestServer()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				get(t, s, "/")
			}
		}
	}()

	// The caller reuses ONE backing array across calls.
	positions := []Position{{Ticker: "AAPL_US_EQ", Name: "Apple Inc"}}
	for i := 0; i < 2000; i++ {
		s.SetMetrics(Metrics{Currency: "GBP", Positions: positions})
		positions[0].Ticker = "VUSA_EQ"
		positions[0].Tracked = i%2 == 0
	}

	close(stop)
	wg.Wait()
}
