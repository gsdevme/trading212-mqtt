package trading212

import (
	"math"
	"os"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestParseAccountSummary(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	a, err := parseAccountSummary(readFixture(t, "summary.json"), now)
	if err != nil {
		t.Fatalf("parseAccountSummary: %v", err)
	}

	if a.ID != 12345678 {
		t.Errorf("ID = %d, want 12345678", a.ID)
	}
	if a.Currency != "GBP" {
		t.Errorf("Currency = %q, want GBP", a.Currency)
	}
	if a.TotalValue != 15234.56 {
		t.Errorf("TotalValue = %v", a.TotalValue)
	}
	if a.FreeCash != 1200.00 {
		t.Errorf("FreeCash = %v, want cash.availableToTrade", a.FreeCash)
	}
	if a.CashInPies != 34.56 {
		t.Errorf("CashInPies = %v", a.CashInPies)
	}
	if a.CashReserved != 100.00 {
		t.Errorf("CashReserved = %v", a.CashReserved)
	}
	if a.Invested != 12500.00 {
		t.Errorf("Invested = %v, want investments.totalCost", a.Invested)
	}
	if a.CurrentValue != 13900.00 {
		t.Errorf("CurrentValue = %v", a.CurrentValue)
	}
	if a.UnrealizedPL != 1400.00 {
		t.Errorf("UnrealizedPL = %v", a.UnrealizedPL)
	}
	if a.RealizedPL != 320.75 {
		t.Errorf("RealizedPL = %v", a.RealizedPL)
	}
	if a.ReturnPct == nil {
		t.Fatal("ReturnPct should be computed")
	}
	// Compare with a tolerance: an untyped constant expression like
	// 1400.0/12500.0*100 is folded at compile time with arbitrary precision and
	// will not bit-match the same arithmetic done on runtime float64 values.
	if want := 11.2; math.Abs(*a.ReturnPct-want) > 1e-9 {
		t.Errorf("ReturnPct = %v, want %v", *a.ReturnPct, want)
	}
	if !a.LastUpdated.Equal(now) {
		t.Errorf("LastUpdated = %v, want %v", a.LastUpdated, now)
	}
}

func TestParsePositions(t *testing.T) {
	ps, err := parsePositions(readFixture(t, "positions.json"), "GBP")
	if err != nil {
		t.Fatalf("parsePositions: %v", err)
	}
	if len(ps) != 3 {
		t.Fatalf("got %d positions, want 3", len(ps))
	}

	aapl := ps[0]
	if aapl.Ticker != "AAPL_US_EQ" || aapl.Name != "Apple Inc." || aapl.ISIN != "US0378331005" {
		t.Errorf("instrument identity not mapped: %+v", aapl)
	}
	if aapl.Quantity != 12.5 || aapl.QuantityInPies != 0 {
		t.Errorf("quantities not mapped: %+v", aapl)
	}
	if aapl.AvgPrice != 180.25 || aapl.CurrentPrice != 212.40 {
		t.Errorf("prices not mapped: %+v", aapl)
	}
	if aapl.Value != 2100.50 || aapl.Cost != 1800.00 ||
		aapl.UnrealizedPL != 300.50 || aapl.FXImpact != -45.20 {
		t.Errorf("wallet impact not mapped: %+v", aapl)
	}
	if want := time.Date(2024, 3, 11, 9, 30, 0, 0, time.UTC); !aapl.Opened.Equal(want) {
		t.Errorf("Opened = %v, want %v", aapl.Opened, want)
	}

	// VUSA_EQ is the same-currency fixture with a non-zero quantityInPies;
	// assert the full field set so that edge case is actually covered, not
	// just documented by the fixture.
	vusa := ps[1]
	if vusa.Ticker != "VUSA_EQ" || vusa.Name != "Vanguard S&P 500 UCITS ETF" || vusa.ISIN != "IE00B3XXRP09" {
		t.Errorf("instrument identity not mapped: %+v", vusa)
	}
	if vusa.Quantity != 100 || vusa.QuantityInPies != 10 {
		t.Errorf("quantities not mapped: %+v", vusa)
	}
	if vusa.AvgPrice != 75.10 || vusa.CurrentPrice != 82.30 {
		t.Errorf("prices not mapped: %+v", vusa)
	}
	if vusa.Value != 8230.00 || vusa.Cost != 7510.00 ||
		vusa.UnrealizedPL != 720.00 || vusa.FXImpact != 0 {
		t.Errorf("wallet impact not mapped: %+v", vusa)
	}
	if want := time.Date(2023, 1, 5, 14, 0, 0, 0, time.UTC); !vusa.Opened.Equal(want) {
		t.Errorf("Opened = %v, want %v", vusa.Opened, want)
	}
}

// The rule most easily broken: prices are in the instrument's currency, wallet
// impact is in the account's.
func TestParsePositionsTwoCurrencyRule(t *testing.T) {
	ps, err := parsePositions(readFixture(t, "positions.json"), "GBP")
	if err != nil {
		t.Fatalf("parsePositions: %v", err)
	}

	aapl := ps[0]
	if aapl.InstrumentCurrency != "USD" {
		t.Errorf("InstrumentCurrency = %q, want USD (prices are USD-denominated)", aapl.InstrumentCurrency)
	}
	if aapl.AccountCurrency != "GBP" {
		t.Errorf("AccountCurrency = %q, want GBP (wallet impact is GBP-denominated)", aapl.AccountCurrency)
	}

	vusa := ps[1]
	if vusa.InstrumentCurrency != "GBP" || vusa.AccountCurrency != "GBP" {
		t.Errorf("same-currency position mismapped: %+v", vusa)
	}
}

// walletImpact.currency is authoritative for the account currency; the caller's
// value is only a fallback for a response that omits it.
func TestParsePositionsPrefersWalletImpactCurrency(t *testing.T) {
	body := []byte(`[{"instrument":{"ticker":"X_EQ","currency":"USD"},
	  "walletImpact":{"currency":"EUR","totalCost":10,"unrealizedProfitLoss":1}}]`)
	ps, err := parsePositions(body, "GBP")
	if err != nil {
		t.Fatalf("parsePositions: %v", err)
	}
	if ps[0].AccountCurrency != "EUR" {
		t.Errorf("AccountCurrency = %q, want EUR from walletImpact.currency", ps[0].AccountCurrency)
	}
}

func TestParsePositionsFallsBackToAccountCurrency(t *testing.T) {
	body := []byte(`[{"instrument":{"ticker":"X_EQ","currency":"USD"},
	  "walletImpact":{"totalCost":10,"unrealizedProfitLoss":1}}]`)
	ps, err := parsePositions(body, "GBP")
	if err != nil {
		t.Fatalf("parsePositions: %v", err)
	}
	if ps[0].AccountCurrency != "GBP" {
		t.Errorf("AccountCurrency = %q, want the GBP fallback", ps[0].AccountCurrency)
	}
}

func TestParsePositionsZeroCostReturnIsNil(t *testing.T) {
	ps, err := parsePositions(readFixture(t, "positions.json"), "GBP")
	if err != nil {
		t.Fatalf("parsePositions: %v", err)
	}
	free := ps[2]
	if free.Ticker != "FREE_EQ" {
		t.Fatalf("expected FREE_EQ as the third fixture, got %q", free.Ticker)
	}
	if free.ReturnPct != nil {
		t.Errorf("ReturnPct = %v, want nil for a zero cost basis", *free.ReturnPct)
	}
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	if _, err := parseAccountSummary([]byte("{nope"), time.Now()); err == nil {
		t.Error("expected an error for malformed summary JSON")
	}
	if _, err := parsePositions([]byte("{nope"), "GBP"); err == nil {
		t.Error("expected an error for malformed positions JSON")
	}
}

func TestParsePositionsEmptyArray(t *testing.T) {
	ps, err := parsePositions([]byte("[]"), "GBP")
	if err != nil {
		t.Fatalf("parsePositions: %v", err)
	}
	if len(ps) != 0 {
		t.Errorf("got %d positions, want 0", len(ps))
	}
}

// A position object that omits walletImpact entirely is not something the
// live API is expected to send, but the parser must not panic on it. Go
// zero-values the nested struct, so this pins the resulting behaviour: the
// account-currency argument is used as the fallback, every wallet-impact
// figure comes back zero, and ReturnPct is nil because cost is zero.
//
// Note this zero-valued result is indistinguishable from a genuine zero-cost
// holding like FREE_EQ in testdata/positions.json — the parser cannot tell
// "walletImpact was omitted" from "walletImpact was present with all zeros",
// and that is an accepted limitation, not a gap to close here.
func TestParsePositionsMissingWalletImpact(t *testing.T) {
	body := []byte(`[{"instrument":{"ticker":"X_EQ","currency":"USD"},"quantity":5}]`)
	ps, err := parsePositions(body, "GBP")
	if err != nil {
		t.Fatalf("parsePositions: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d positions, want 1", len(ps))
	}
	p := ps[0]
	if p.AccountCurrency != "GBP" {
		t.Errorf("AccountCurrency = %q, want the GBP fallback", p.AccountCurrency)
	}
	if p.Value != 0 || p.Cost != 0 || p.UnrealizedPL != 0 || p.FXImpact != 0 {
		t.Errorf("wallet impact figures = %+v, want all zero", p)
	}
	if p.ReturnPct != nil {
		t.Errorf("ReturnPct = %v, want nil for a zero cost basis", *p.ReturnPct)
	}
	if !p.Opened.IsZero() {
		t.Errorf("Opened = %v, want the zero time for an omitted createdAt", p.Opened)
	}
}
