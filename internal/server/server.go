// Package server exposes the daemon's HTTP surface: a human-readable status page
// on / plus liveness (/healthz) and readiness (/readyz) probes. Liveness is
// always OK while the process runs; readiness flips ready after the first
// successful publish and not-ready after a configurable number of consecutive
// poll failures. See docs/specs/06-lifecycle-health.md.
package server

import (
	"net/http"
	"slices"
	"sync"
	"time"
)

// Config configures the status server. It is deliberately decoupled from the
// other packages: serve.go maps domain values in, so nothing is imported here.
type Config struct {
	ReadyFailureThreshold int
	PollInterval          time.Duration
	Mode                  string
}

// Position is one row of the status page's holdings table. It is a view model,
// not the domain type: this package imports nothing from the other packages, so
// serve.go maps the values in.
type Position struct {
	Ticker  string
	Name    string
	Value   float64
	Tracked bool // has a Home Assistant device, i.e. matched TICKERS
}

// Metrics is a snapshot of the latest figures shown on the / page. The zero
// value (Set == false) renders no metrics section.
//
// ReturnPct is a pointer so "not computable" renders differently from 0%.
type Metrics struct {
	Set bool

	Currency     string
	TotalValue   float64
	FreeCash     float64
	Invested     float64
	UnrealizedPL float64
	ReturnPct    *float64

	// PositionCount is every open position; TrackedCount is how many of them
	// have a Home Assistant device.
	PositionCount int
	TrackedCount  int

	// Positions is one row per held position, in the order the page renders
	// them.
	Positions []Position

	LastUpdated time.Time
}

// Server tracks readiness from poll outcomes and holds a concurrency-safe
// snapshot of operational status for the / page.
type Server struct {
	mu                  sync.Mutex
	ready               bool
	consecutiveFailures int
	threshold           int

	accountSet bool
	accountID  int64
	currency   string

	metrics Metrics

	cfg       Config
	startedAt time.Time
}

// New returns a Server that flips not-ready after cfg.ReadyFailureThreshold
// consecutive failures. A threshold below 1 is treated as 1.
func New(cfg Config) *Server {
	if cfg.ReadyFailureThreshold < 1 {
		cfg.ReadyFailureThreshold = 1
	}
	return &Server{threshold: cfg.ReadyFailureThreshold, cfg: cfg, startedAt: time.Now()}
}

// MarkSuccess records a successful publish: ready becomes true and the failure
// counter resets.
func (s *Server) MarkSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = true
	s.consecutiveFailures = 0
}

// MarkFailure records a poll failure; readiness flips off once the threshold of
// consecutive failures is reached.
func (s *Server) MarkFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutiveFailures++
	if s.consecutiveFailures >= s.threshold {
		s.ready = false
	}
}

// Ready reports the current readiness state.
func (s *Server) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// SetAccount records the account identity for the status page. The server starts
// listening before the first API call, so the page renders "initialising" until
// this runs.
func (s *Server) SetAccount(id int64, currency string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountSet = true
	s.accountID = id
	s.currency = currency
}

// SetMetrics records the latest figures for the status page, called after each
// successful publish.
func (s *Server) SetMetrics(m Metrics) {
	// Copy the pointee: the caller keeps ownership of its own float, so a
	// caller that reuses one backing variable across polls cannot race with
	// handleRoot dereferencing this pointer after the lock is released.
	if m.ReturnPct != nil {
		v := *m.ReturnPct
		m.ReturnPct = &v
	}
	// Same reasoning for the slice: the caller keeps ownership of its backing
	// array, and handleRoot ranges over this one after releasing the lock.
	m.Positions = slices.Clone(m.Positions)

	s.mu.Lock()
	defer s.mu.Unlock()
	m.Set = true
	s.metrics = m
}

// Handler returns the mux serving /, /healthz and /readyz. See routes.go.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, s)
	return mux
}

func (s *Server) handleLivez(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if s.Ready() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not ready"))
}
