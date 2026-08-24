package trading212

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, APIKey: "key", APISecret: "secret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Remove pacing so tests don't spend real time; spacing itself is covered
	// by the limiter's own tests.
	c.summaryLimiter = NewLimiter(0)
	c.positionsLimiter = NewLimiter(0)
	return c, srv
}

func TestAccountSummaryRequest(t *testing.T) {
	var gotPath, gotAuth, gotAccept string
	var gotMethod string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		b, _ := os.ReadFile("testdata/summary.json")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))

	a, err := c.AccountSummary(context.Background())
	if err != nil {
		t.Fatalf("AccountSummary: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if gotPath != pathAccountSummary {
		t.Errorf("path = %q, want %q", gotPath, pathAccountSummary)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("key:secret"))
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if a.ID != 12345678 || a.Currency != "GBP" {
		t.Errorf("summary not parsed: %+v", a)
	}
	if a.LastUpdated.IsZero() {
		t.Error("LastUpdated should be stamped by the client")
	}
}

func TestLegacyAuthHeaderWhenSecretIsEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{BaseURL: srv.URL, APIKey: "legacy-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.summaryLimiter = NewLimiter(0)

	if _, err := c.AccountSummary(context.Background()); err != nil {
		t.Fatalf("AccountSummary: %v", err)
	}
	if gotAuth != "legacy-key" {
		t.Errorf("Authorization = %q, want the bare key", gotAuth)
	}
}

func TestPositionsRequestSendsNoTickerParameter(t *testing.T) {
	var gotQuery string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		b, _ := os.ReadFile("testdata/positions.json")
		_, _ = w.Write(b)
	}))

	ps, err := c.Positions(context.Background(), "GBP")
	if err != nil {
		t.Fatalf("Positions: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want none — filtering happens client-side", gotQuery)
	}
	if len(ps) != 3 {
		t.Errorf("got %d positions, want 3", len(ps))
	}
}

func TestUnauthorizedIsSentinel(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			_, err := c.AccountSummary(context.Background())
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("error = %v, want ErrUnauthorized", err)
			}
		})
	}
}

// REQ-T2-07: a 429 must both surface the advertised reset time *and* hold that
// endpoint's limiter until it. The hold is the half that matters operationally
// — limits are enforced per account, so ignoring the reset throttles every
// other tool sharing the credentials — so it is asserted by observing the
// limiter on a fake clock, not merely by reading the error.
func TestRateLimitErrorCarriesResetAndHoldsLimiter(t *testing.T) {
	// The limiter runs on a fake clock so the hold is observable without
	// spending 30 real seconds. Its spacing is zero: any wait the limiter
	// reports afterwards can only come from the 429 hold.
	lim, clk := newTestLimiter(0)
	resetAt := clk.now.Add(30 * time.Second)

	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerRateLimitReset, strconv.FormatInt(resetAt.Unix(), 10))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	c.summaryLimiter = lim

	_, err := c.AccountSummary(context.Background())
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("error = %v, want *RateLimitError", err)
	}
	if !rle.ResetAt.Equal(resetAt) {
		t.Errorf("ResetAt = %v, want %v", rle.ResetAt, resetAt)
	}

	// The next call on that endpoint must block until the reset.
	if err := lim.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(clk.slept) != 1 || clk.slept[0] != 30*time.Second {
		t.Fatalf("limiter slept %v after the 429, want a single 30s hold until ResetAt", clk.slept)
	}
	if clk.now.Before(resetAt) {
		t.Errorf("limiter released at %v, want no earlier than ResetAt %v", clk.now, resetAt)
	}
}

func TestServerErrorIsPlainError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	_, err := c.AccountSummary(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Error("a 500 must not be reported as unauthorized")
	}
}

func TestGetReturnsRawBody(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"raw":true}`))
	}))
	b, err := c.Get(context.Background(), "/equity/positions")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(b) != `{"raw":true}` {
		t.Errorf("body = %q", b)
	}
}

func TestNewRejectsMissingConfig(t *testing.T) {
	if _, err := New(Config{APIKey: "k"}); err == nil {
		t.Error("expected an error when BaseURL is empty")
	}
	if _, err := New(Config{BaseURL: "http://x"}); err == nil {
		t.Error("expected an error when APIKey is empty")
	}
}
