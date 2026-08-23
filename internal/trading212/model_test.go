package trading212

import (
	"encoding/json"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"AAPL_US_EQ", "aapl_us_eq"},
		{"VUSA_EQ", "vusa_eq"},
		{"BRK.B_US_EQ", "brk_b_us_eq"},
		{"IUSAl_EQ", "iusal_eq"},
		{"a b/c#d+e", "a_b_c_d_e"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := Slug(tc.in); got != tc.want {
				t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestReturnPct(t *testing.T) {
	got := ReturnPct(25, 100)
	if got == nil || *got != 25 {
		t.Fatalf("ReturnPct(25, 100) = %v, want 25", got)
	}
	if ReturnPct(25, 0) != nil {
		t.Error("ReturnPct with a zero cost basis must be nil, not an infinity")
	}
	neg := ReturnPct(-10, 200)
	if neg == nil || *neg != -5 {
		t.Fatalf("ReturnPct(-10, 200) = %v, want -5", neg)
	}
}

func TestParseWhitelist(t *testing.T) {
	t.Run("empty tracks nothing", func(t *testing.T) {
		w := ParseWhitelist("")
		if w.All {
			t.Error("empty must not mean all")
		}
		if w.Includes("AAPL_US_EQ") {
			t.Error("empty must include nothing")
		}
		if len(w.Static()) != 0 {
			t.Errorf("Static() = %v, want empty", w.Static())
		}
	})

	t.Run("star tracks everything", func(t *testing.T) {
		w := ParseWhitelist("*")
		if !w.All {
			t.Error("* must set All")
		}
		if !w.Includes("ANYTHING_EQ") {
			t.Error("* must include any ticker")
		}
		if w.Static() != nil {
			t.Errorf("Static() = %v, want nil when All", w.Static())
		}
	})

	t.Run("list tracks exactly those tickers", func(t *testing.T) {
		w := ParseWhitelist(" aapl_us_eq , VUSA_EQ ,, ")
		if w.All {
			t.Error("a list must not set All")
		}
		if !w.Includes("AAPL_US_EQ") {
			t.Error("matching must be case-insensitive")
		}
		if !w.Includes("vusa_eq") {
			t.Error("matching must be case-insensitive")
		}
		if w.Includes("MSFT_US_EQ") {
			t.Error("must not include an unlisted ticker")
		}
		if got := w.Static(); len(got) != 2 {
			t.Errorf("Static() = %v, want 2 entries with blanks dropped", got)
		}
	})
}

// TestWhitelistStaticPreservesCasing protects a specific ruling: Whitelist
// stores tickers exactly as the operator typed them and only matches
// case-insensitively. Task 10 uses Static() to name Home Assistant devices
// for whitelisted tickers that are not currently held, so if this were to
// lower-case at parse time, a user who configures TICKERS=NOTHELD_EQ would
// get a device named using "notheld_eq" instead of the casing they actually
// typed. Do not "simplify" ParseWhitelist by lower-casing again.
func TestWhitelistStaticPreservesCasing(t *testing.T) {
	w := ParseWhitelist("NOTHELD_EQ,vusa_eq")
	want := []string{"NOTHELD_EQ", "vusa_eq"}
	got := w.Static()
	if len(got) != len(want) {
		t.Fatalf("Static() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Static()[%d] = %q, want %q (original casing must be preserved)", i, got[i], want[i])
		}
	}
}

func TestAccountSummaryJSONTags(t *testing.T) {
	pct := 12.5
	a := AccountSummary{
		ID: 42, Currency: "GBP", TotalValue: 1000, FreeCash: 100,
		CashInPies: 5, CashReserved: 2, Invested: 800, CurrentValue: 900,
		UnrealizedPL: 100, RealizedPL: 50, ReturnPct: &pct,
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Entity keys in internal/homeassistant read these exact names.
	for _, k := range []string{
		"total_value", "free_cash", "cash_in_pies", "cash_reserved",
		"invested", "current_value", "unrealized_pl", "realized_pl",
		"return_pct", "last_updated", "currency",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("state document is missing key %q", k)
		}
	}
}

func TestPositionJSONTags(t *testing.T) {
	p := Position{Ticker: "AAPL_US_EQ", Name: "Apple Inc."}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{
		"ticker", "name", "isin", "instrument_currency", "account_currency",
		"quantity", "quantity_in_pies", "avg_price", "current_price",
		"value", "cost", "unrealized_pl", "fx_impact", "return_pct", "opened",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("state document is missing key %q", k)
		}
	}
}

func TestSnapshotHeld(t *testing.T) {
	s := Snapshot{Positions: []Position{{Ticker: "AAPL_US_EQ"}, {Ticker: "VUSA_EQ"}}}
	p, ok := s.Held("VUSA_EQ")
	if !ok || p.Ticker != "VUSA_EQ" {
		t.Fatalf("Held(VUSA_EQ) = %v, %v", p, ok)
	}
	if _, ok := s.Held("MSFT_US_EQ"); ok {
		t.Error("Held must report false for a position that is not held")
	}
}
