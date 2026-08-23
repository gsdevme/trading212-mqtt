// Package mock implements an in-process stand-in for the Trading 212 Public API.
// It serves the two endpoints the client uses with canned data built from
// Options, so scenarios can vary account and position state. It is shared by the
// godog acceptance suite and the standalone `mock` subcommand, letting the full
// pipeline run without credentials. See docs/specs/07-testing.md.
package mock

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Address is the default listen address for the standalone mock. It must match
// the MOCK_URL default in internal/config.
const Address = ":8090"

// PositionOption is one canned holding.
type PositionOption struct {
	Ticker             string
	Name               string
	ISIN               string
	InstrumentCurrency string

	Quantity       float64
	QuantityInPies float64

	AvgPrice     float64
	CurrentPrice float64

	Value        float64
	Cost         float64
	UnrealizedPL float64
	FXImpact     float64

	Opened time.Time
}

// Options configure the canned account and positions.
type Options struct {
	AccountID int64
	Currency  string

	TotalValue   float64
	FreeCash     float64
	CashInPies   float64
	CashReserved float64

	Invested     float64
	CurrentValue float64
	UnrealizedPL float64
	RealizedPL   float64

	Positions []PositionOption

	Logger *slog.Logger
}

// Defaults returns a GBP account holding three positions. The set deliberately
// encodes the edge cases the specs require: a cross-currency USD holding, a
// same-currency GBP holding, and a zero-cost free share.
func Defaults() Options {
	return Options{
		AccountID:    12345678,
		Currency:     "GBP",
		TotalValue:   15234.56,
		FreeCash:     1200.00,
		CashInPies:   34.56,
		CashReserved: 100.00,
		Invested:     12500.00,
		CurrentValue: 13900.00,
		UnrealizedPL: 1400.00,
		RealizedPL:   320.75,
		Positions: []PositionOption{
			{
				Ticker: "AAPL_US_EQ", Name: "Apple Inc.", ISIN: "US0378331005",
				InstrumentCurrency: "USD",
				Quantity:           12.5,
				AvgPrice:           180.25, CurrentPrice: 212.40,
				Value: 2100.50, Cost: 1800.00, UnrealizedPL: 300.50, FXImpact: -45.20,
				Opened: time.Date(2024, 3, 11, 9, 30, 0, 0, time.UTC),
			},
			{
				Ticker: "VUSA_EQ", Name: "Vanguard S&P 500 UCITS ETF", ISIN: "IE00B3XXRP09",
				InstrumentCurrency: "GBP",
				Quantity:           100, QuantityInPies: 10,
				AvgPrice: 75.10, CurrentPrice: 82.30,
				Value: 8230.00, Cost: 7510.00, UnrealizedPL: 720.00,
				Opened: time.Date(2023, 1, 5, 14, 0, 0, 0, time.UTC),
			},
			{
				Ticker: "FREE_EQ", Name: "Free Share Ltd", ISIN: "GB00FREE0001",
				InstrumentCurrency: "GBP",
				Quantity:           1,
				AvgPrice:           0, CurrentPrice: 3.40,
				Value: 3.40, Cost: 0, UnrealizedPL: 3.40,
				Opened: time.Date(2025, 6, 1, 8, 0, 0, 0, time.UTC),
			},
		},
		Logger: slog.Default(),
	}
}

// Server is the mock API. Create with New and mount Handler().
type Server struct {
	mu   sync.Mutex
	opts Options
	fail bool

	summaryHits   int
	positionsHits int
}

// New builds a mock server with the given canned state.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Server{opts: opts}
}

// SetOptions replaces the canned state, used between scenarios.
func (s *Server) SetOptions(o Options) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o.Logger == nil {
		o.Logger = s.opts.Logger
	}
	s.opts = o
}

// SetFail toggles whether both endpoints return HTTP 500, so callers can test
// graceful degradation and readiness.
func (s *Server) SetFail(fail bool) {
	s.mu.Lock()
	s.fail = fail
	s.mu.Unlock()
}

// Counts returns how many times each endpoint has been called.
func (s *Server) Counts() (summary, positions int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryHits, s.positionsHits
}

// Handler returns the HTTP handler for the mock API. See routes.go for the route
// table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, s)
	return mux
}

func (s *Server) handleAccountSummary(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.summaryHits++
	o, fail := s.opts, s.fail
	s.mu.Unlock()

	if fail {
		writeRateLimitHeaders(w)
		http.Error(w, "upstream error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         o.AccountID,
		"currency":   o.Currency,
		"totalValue": o.TotalValue,
		"cash": map[string]any{
			"availableToTrade":  o.FreeCash,
			"inPies":            o.CashInPies,
			"reservedForOrders": o.CashReserved,
		},
		"investments": map[string]any{
			"currentValue":         o.CurrentValue,
			"totalCost":            o.Invested,
			"unrealizedProfitLoss": o.UnrealizedPL,
			"realizedProfitLoss":   o.RealizedPL,
		},
	})
}

func (s *Server) handlePositions(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.positionsHits++
	o, fail := s.opts, s.fail
	s.mu.Unlock()

	if fail {
		writeRateLimitHeaders(w)
		http.Error(w, "upstream error", http.StatusInternalServerError)
		return
	}

	out := make([]map[string]any, 0, len(o.Positions))
	for _, p := range o.Positions {
		out = append(out, map[string]any{
			"instrument": map[string]any{
				"ticker":   p.Ticker,
				"name":     p.Name,
				"isin":     p.ISIN,
				"currency": p.InstrumentCurrency,
			},
			"quantity":                    p.Quantity,
			"quantityAvailableForTrading": p.Quantity - p.QuantityInPies,
			"quantityInPies":              p.QuantityInPies,
			"averagePricePaid":            p.AvgPrice,
			"currentPrice":                p.CurrentPrice,
			"createdAt":                   p.Opened.Format(time.RFC3339),
			"walletImpact": map[string]any{
				"currency":             o.Currency,
				"currentValue":         p.Value,
				"totalCost":            p.Cost,
				"unrealizedProfitLoss": p.UnrealizedPL,
				"fxImpact":             p.FXImpact,
			},
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// writeRateLimitHeaders emits a healthy budget, mirroring the real API so the
// client's limiter exercises its Observe path in tests.
func writeRateLimitHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Ratelimit-Limit", "50")
	w.Header().Set("X-Ratelimit-Period", "60")
	w.Header().Set("X-Ratelimit-Remaining", "49")
	w.Header().Set("X-Ratelimit-Used", "1")
	w.Header().Set("X-Ratelimit-Reset", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	writeRateLimitHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
