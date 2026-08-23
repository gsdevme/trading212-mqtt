package mock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServesAccountSummary(t *testing.T) {
	srv := httptest.NewServer(New(Defaults()).Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/v0/equity/account/summary")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"id", "currency", "totalValue", "cash", "investments"} {
		if _, ok := m[k]; !ok {
			t.Errorf("summary response missing %q", k)
		}
	}
}

func TestServesPositions(t *testing.T) {
	srv := httptest.NewServer(New(Defaults()).Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/v0/equity/positions")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ps []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(ps) != len(Defaults().Positions) {
		t.Fatalf("got %d positions, want %d", len(ps), len(Defaults().Positions))
	}
	inst, ok := ps[0]["instrument"].(map[string]any)
	if !ok {
		t.Fatal("position missing instrument object")
	}
	if inst["ticker"] == "" {
		t.Error("position missing ticker")
	}
	if _, ok := ps[0]["walletImpact"]; !ok {
		t.Error("position missing walletImpact object")
	}
}

// The default fixture set must encode the edge cases the specs require.
func TestDefaultsCoverEdgeCases(t *testing.T) {
	d := Defaults()

	var crossCurrency, zeroCost bool
	for _, p := range d.Positions {
		if p.InstrumentCurrency != d.Currency {
			crossCurrency = true
		}
		if p.Cost == 0 {
			zeroCost = true
		}
	}
	if !crossCurrency {
		t.Error("defaults must include a cross-currency holding")
	}
	if !zeroCost {
		t.Error("defaults must include a zero-cost position")
	}
}

func TestRateLimitHeadersArePresent(t *testing.T) {
	srv := httptest.NewServer(New(Defaults()).Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/v0/equity/positions")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	for _, h := range []string{"X-Ratelimit-Limit", "X-Ratelimit-Remaining", "X-Ratelimit-Reset"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("missing %s header", h)
		}
	}
}

func TestSetFailMakesEndpointsFail(t *testing.T) {
	m := New(Defaults())
	m.SetFail(true)
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/v0/equity/account/summary")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestCountsTrackRequests(t *testing.T) {
	m := New(Defaults())
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)

	for i := 0; i < 2; i++ {
		r, _ := http.Get(srv.URL + "/api/v0/equity/account/summary")
		_ = r.Body.Close()
	}
	r, _ := http.Get(srv.URL + "/api/v0/equity/positions")
	_ = r.Body.Close()

	summary, positions := m.Counts()
	if summary != 2 || positions != 1 {
		t.Errorf("Counts() = (%d, %d), want (2, 1)", summary, positions)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	srv := httptest.NewServer(New(Defaults()).Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/v0/equity/orders")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — the mock must not offer a write surface", resp.StatusCode)
	}
}
