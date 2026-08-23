// Package trading212 is the Trading 212 Public API client and its domain model.
// The API contract lives in docs/specs/01-trading212-api.md and the
// trading212-api skill; the types here are described by
// docs/specs/02-domain-model.md.
//
// This package is strictly read-only: it calls two GET endpoints and can never
// place, modify or cancel anything.
package trading212

import (
	"strings"
	"time"
)

// AccountSummary is the account-level snapshot published to the account device's
// state topic. Every monetary field is denominated in Currency, the account's
// primary currency.
//
// The JSON tags are the field names Home Assistant entities read via
// value_template, so they are part of the published contract — renaming one
// breaks every entity that reads it.
type AccountSummary struct {
	ID       int64  `json:"-"`
	Currency string `json:"currency"`

	TotalValue   float64 `json:"total_value"`
	FreeCash     float64 `json:"free_cash"`
	CashInPies   float64 `json:"cash_in_pies"`
	CashReserved float64 `json:"cash_reserved"`

	Invested     float64 `json:"invested"`
	CurrentValue float64 `json:"current_value"`
	UnrealizedPL float64 `json:"unrealized_pl"`
	RealizedPL   float64 `json:"realized_pl"`

	// ReturnPct is nil when the cost basis is zero, distinguishing "not
	// computable" from a genuine 0%.
	ReturnPct *float64 `json:"return_pct"`

	LastUpdated time.Time `json:"last_updated"`
}

// Position is one open holding, published to that ticker's own state topic.
//
// Two currencies are in play: AvgPrice and CurrentPrice are denominated in
// InstrumentCurrency, while Value, Cost, UnrealizedPL and FxImpact are
// denominated in AccountCurrency. See docs/specs/01-trading212-api.md.
type Position struct {
	Ticker string `json:"ticker"`
	Name   string `json:"name"`
	ISIN   string `json:"isin"`

	InstrumentCurrency string `json:"instrument_currency"`
	AccountCurrency    string `json:"account_currency"`

	Quantity       float64 `json:"quantity"`
	QuantityInPies float64 `json:"quantity_in_pies"`

	// Instrument-currency figures.
	AvgPrice     float64 `json:"avg_price"`
	CurrentPrice float64 `json:"current_price"`

	// Account-currency figures.
	Value        float64 `json:"value"`
	Cost         float64 `json:"cost"`
	UnrealizedPL float64 `json:"unrealized_pl"`
	FxImpact     float64 `json:"fx_impact"`

	// ReturnPct is nil when Cost is zero.
	ReturnPct *float64 `json:"return_pct"`

	Opened time.Time `json:"opened"`
}

// Snapshot is the result of one poll: the two API responses, already parsed.
type Snapshot struct {
	Account   AccountSummary
	Positions []Position
}

// Held reports the position for a ticker if the account currently holds it.
// Matching is case-insensitive because whitelists are user-typed.
func (s Snapshot) Held(ticker string) (Position, bool) {
	for _, p := range s.Positions {
		if strings.EqualFold(p.Ticker, ticker) {
			return p, true
		}
	}
	return Position{}, false
}

// ReturnPct computes a percentage return, returning nil when the cost basis is
// zero. A zero cost basis is not a 0% return — it has no defined return at all,
// and publishing a null lets Home Assistant render the entity as unknown.
func ReturnPct(unrealized, cost float64) *float64 {
	if cost == 0 {
		return nil
	}
	pct := unrealized / cost * 100
	return &pct
}

// Whitelist selects which positions get their own Home Assistant device.
//
// The zero value tracks nothing, which is the safe default: an account with
// hundreds of holdings would otherwise create hundreds of devices on first run.
type Whitelist struct {
	// All tracks every currently-held position, discovering devices as
	// positions appear.
	All bool
	// tickers holds the explicit list exactly as the operator typed it.
	// Matching against it is case-insensitive (see Includes), but the
	// original casing is preserved because Task 10 uses Static() to name
	// Home Assistant devices for whitelisted tickers that are not currently
	// held, and those device names should reflect what the operator typed.
	tickers []string
}

// ParseWhitelist parses the TICKERS environment value. An empty string tracks
// nothing; "*" tracks everything; anything else is a comma-separated list of
// tickers, with surrounding whitespace and empty entries ignored. Tickers are
// stored exactly as given — see the tickers field doc for why.
func ParseWhitelist(s string) Whitelist {
	s = strings.TrimSpace(s)
	if s == "" {
		return Whitelist{}
	}
	if s == "*" {
		return Whitelist{All: true}
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return Whitelist{tickers: out}
}

// Includes reports whether a ticker should be tracked. Matching is
// case-insensitive.
func (w Whitelist) Includes(ticker string) bool {
	if w.All {
		return true
	}
	for _, want := range w.tickers {
		if strings.EqualFold(want, ticker) {
			return true
		}
	}
	return false
}

// Static returns the explicitly listed tickers, or nil when the whitelist
// tracks everything. Callers use it to publish placeholder discovery at startup
// for tickers that are not currently held.
func (w Whitelist) Static() []string {
	if w.All {
		return nil
	}
	return append([]string(nil), w.tickers...)
}

// Slug normalises a ticker for use in MQTT topics and Home Assistant object ids,
// both of which restrict the character set. Everything outside [a-z0-9_] becomes
// an underscore, so "BRK.B_US_EQ" becomes "brk_b_us_eq".
func Slug(ticker string) string {
	var b strings.Builder
	b.Grow(len(ticker))
	for _, r := range strings.ToLower(ticker) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
