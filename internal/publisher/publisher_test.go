package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gsdevme/trading212-mqtt/internal/homeassistant"
	"github.com/gsdevme/trading212-mqtt/internal/trading212"
)

func haConfig() homeassistant.Config {
	return homeassistant.Config{
		DiscoveryPrefix: "homeassistant",
		TopicPrefix:     "trading212",
		AccountID:       12345678,
		Currency:        "GBP",
	}
}

func testSnapshot() trading212.Snapshot {
	pct := 11.2
	return trading212.Snapshot{
		Account: trading212.AccountSummary{
			ID: 12345678, Currency: "GBP", TotalValue: 15234.56, FreeCash: 1200,
			Invested: 12500, CurrentValue: 13900, UnrealizedPL: 1400,
			ReturnPct: &pct, LastUpdated: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
		},
		Positions: []trading212.Position{
			{
				Ticker: "AAPL_US_EQ", Name: "Apple Inc.",
				InstrumentCurrency: "USD", AccountCurrency: "GBP",
				Quantity: 12.5, AvgPrice: 180.25, CurrentPrice: 212.40,
				Value: 2100.50, Cost: 1800, UnrealizedPL: 300.50,
			},
			{
				Ticker: "VUSA_EQ", Name: "Vanguard S&P 500 UCITS ETF",
				InstrumentCurrency: "GBP", AccountCurrency: "GBP",
				Quantity: 100, Value: 8230, Cost: 7510, UnrealizedPL: 720,
			},
		},
	}
}

func mustDecode(t *testing.T, rec Record) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Payload, &m); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return m
}

func TestPublishDiscoveryPublishesAccountEntitiesRetained(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist(""))

	if err := s.PublishDiscovery(context.Background()); err != nil {
		t.Fatalf("PublishDiscovery: %v", err)
	}

	topic := "homeassistant/sensor/t212_12345678/total_value/config"
	r, ok := rec.Get(topic)
	if !ok {
		t.Fatalf("missing %s; got %v", topic, rec.Topics())
	}
	if !r.Retain {
		t.Error("discovery must be published retained")
	}
}

// REQ-HA-09: a statically whitelisted ticker gets a device immediately, even
// before it is ever held.
func TestPublishDiscoveryPublishesPlaceholdersForStaticWhitelist(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist("NOTHELD_EQ"))

	if err := s.PublishDiscovery(context.Background()); err != nil {
		t.Fatalf("PublishDiscovery: %v", err)
	}

	topic := "homeassistant/sensor/t212_12345678_notheld_eq/value/config"
	r, ok := rec.Get(topic)
	if !ok {
		t.Fatalf("missing placeholder discovery %s; got %v", topic, rec.Topics())
	}
	p := mustDecode(t, r)
	dev, _ := p["device"].(map[string]any)
	if dev["name"] != "Trading 212 – NOTHELD_EQ" {
		t.Errorf("placeholder device name = %v, want the raw ticker", dev["name"])
	}
	// Placeholder currency falls back to the account currency until real
	// instrument metadata arrives.
	if p["unit_of_measurement"] != "GBP" {
		t.Errorf("placeholder unit = %v, want GBP", p["unit_of_measurement"])
	}

	av, ok := rec.Get("trading212/12345678/positions/notheld_eq/availability")
	if !ok {
		t.Fatal("placeholder must publish its availability topic")
	}
	if string(av.Payload) != "offline" {
		t.Errorf("placeholder availability = %q, want offline", av.Payload)
	}
}

func TestPublishDiscoveryPublishesNoPlaceholdersWhenWhitelistIsAll(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist("*"))

	if err := s.PublishDiscovery(context.Background()); err != nil {
		t.Fatalf("PublishDiscovery: %v", err)
	}
	for _, topic := range rec.Topics() {
		if strings.Contains(topic, "positions/") || strings.Contains(topic, "t212_12345678_") {
			t.Errorf("unexpected position topic %s before any poll", topic)
		}
	}
}

func TestPublishSnapshotPublishesAccountState(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist(""))

	if err := s.PublishSnapshot(context.Background(), testSnapshot()); err != nil {
		t.Fatalf("PublishSnapshot: %v", err)
	}

	r, ok := rec.Get("trading212/12345678/state")
	if !ok {
		t.Fatal("missing account state topic")
	}
	if !r.Retain {
		t.Error("state must be retained")
	}
	m := mustDecode(t, r)
	if m["free_cash"] != 1200.0 {
		t.Errorf("free_cash = %v", m["free_cash"])
	}
	if m["currency"] != "GBP" {
		t.Errorf("currency = %v", m["currency"])
	}
}

// REQ-SC-06: only whitelisted positions are published.
func TestPublishSnapshotFiltersByWhitelist(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist("AAPL_US_EQ"))

	if err := s.PublishSnapshot(context.Background(), testSnapshot()); err != nil {
		t.Fatalf("PublishSnapshot: %v", err)
	}

	if _, ok := rec.Get("trading212/12345678/positions/aapl_us_eq/state"); !ok {
		t.Error("whitelisted position should be published")
	}
	if _, ok := rec.Get("trading212/12345678/positions/vusa_eq/state"); ok {
		t.Error("non-whitelisted position must not be published")
	}
}

func TestPublishSnapshotWithEmptyWhitelistPublishesNoPositions(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist(""))

	if err := s.PublishSnapshot(context.Background(), testSnapshot()); err != nil {
		t.Fatalf("PublishSnapshot: %v", err)
	}
	for _, topic := range rec.Topics() {
		if strings.Contains(topic, "/positions/") {
			t.Errorf("unexpected position topic %s with an empty whitelist", topic)
		}
	}
}

func TestPublishSnapshotStarTracksEveryHeldPosition(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist("*"))

	if err := s.PublishSnapshot(context.Background(), testSnapshot()); err != nil {
		t.Fatalf("PublishSnapshot: %v", err)
	}
	for _, ticker := range []string{"aapl_us_eq", "vusa_eq"} {
		if _, ok := rec.Get("trading212/12345678/positions/" + ticker + "/state"); !ok {
			t.Errorf("position %s should be published under *", ticker)
		}
	}
	// Discovery is emitted lazily, at the poll that first sees the position.
	if _, ok := rec.Get("homeassistant/sensor/t212_12345678_vusa_eq/value/config"); !ok {
		t.Error("discovery should be published on first sighting")
	}
}

func TestPublishSnapshotPositionStateAndAvailability(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist("AAPL_US_EQ"))

	if err := s.PublishSnapshot(context.Background(), testSnapshot()); err != nil {
		t.Fatalf("PublishSnapshot: %v", err)
	}

	st, _ := rec.Get("trading212/12345678/positions/aapl_us_eq/state")
	m := mustDecode(t, st)
	if m["ticker"] != "AAPL_US_EQ" {
		t.Errorf("ticker = %v", m["ticker"])
	}
	if m["instrument_currency"] != "USD" || m["account_currency"] != "GBP" {
		t.Errorf("currencies not published: %v / %v", m["instrument_currency"], m["account_currency"])
	}

	av, ok := rec.Get("trading212/12345678/positions/aapl_us_eq/availability")
	if !ok || string(av.Payload) != "online" {
		t.Errorf("availability = %q, want online for a held position", av.Payload)
	}
}

// REQ-HA-08: a tracked position that disappears flips to offline and keeps its
// discovery configs.
func TestPublishSnapshotMarksSoldPositionOffline(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist("AAPL_US_EQ"))
	ctx := context.Background()

	if err := s.PublishSnapshot(ctx, testSnapshot()); err != nil {
		t.Fatalf("first PublishSnapshot: %v", err)
	}

	// Second poll: the holding is gone.
	sold := testSnapshot()
	sold.Positions = nil
	if err := s.PublishSnapshot(ctx, sold); err != nil {
		t.Fatalf("second PublishSnapshot: %v", err)
	}

	av, _ := rec.Get("trading212/12345678/positions/aapl_us_eq/availability")
	if string(av.Payload) != "offline" {
		t.Errorf("availability = %q, want offline after the position disappeared", av.Payload)
	}
	if !av.Retain {
		t.Error("availability must be retained")
	}
	if _, ok := rec.Get("homeassistant/sensor/t212_12345678_aapl_us_eq/value/config"); !ok {
		t.Error("discovery configs must be retained, not deleted, when a position disappears")
	}
}

// REQ-HA-09: placeholder discovery is republished with real instrument metadata
// the first time the position is actually seen.
func TestPlaceholderDiscoveryIsRefinedOnFirstSighting(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist("AAPL_US_EQ"))
	ctx := context.Background()

	if err := s.PublishDiscovery(ctx); err != nil {
		t.Fatalf("PublishDiscovery: %v", err)
	}
	before := mustDecode(t, mustGet(t, rec, "homeassistant/sensor/t212_12345678_aapl_us_eq/avg_price/config"))
	if before["unit_of_measurement"] != "GBP" {
		t.Fatalf("placeholder avg_price unit = %v, want the GBP fallback", before["unit_of_measurement"])
	}

	if err := s.PublishSnapshot(ctx, testSnapshot()); err != nil {
		t.Fatalf("PublishSnapshot: %v", err)
	}
	after := mustDecode(t, mustGet(t, rec, "homeassistant/sensor/t212_12345678_aapl_us_eq/avg_price/config"))
	if after["unit_of_measurement"] != "USD" {
		t.Errorf("refined avg_price unit = %v, want USD", after["unit_of_measurement"])
	}
	dev, _ := after["device"].(map[string]any)
	if dev["name"] != "Trading 212 – Apple Inc." {
		t.Errorf("refined device name = %v", dev["name"])
	}
}

// Discovery must not be republished on every poll — only when the metadata that
// shapes it actually changes.
func TestDiscoveryIsNotRepublishedWhenUnchanged(t *testing.T) {
	counting := &countingPublisher{inner: NewRecordingPublisher()}
	s := New(counting, haConfig(), trading212.ParseWhitelist("AAPL_US_EQ"))
	ctx := context.Background()

	if err := s.PublishSnapshot(ctx, testSnapshot()); err != nil {
		t.Fatalf("PublishSnapshot: %v", err)
	}
	firstPass := counting.discoveryCount()

	if err := s.PublishSnapshot(ctx, testSnapshot()); err != nil {
		t.Fatalf("PublishSnapshot: %v", err)
	}
	if got := counting.discoveryCount(); got != firstPass {
		t.Errorf("discovery publishes = %d after a second identical poll, want %d", got, firstPass)
	}
}

func TestPublishAvailability(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist(""))

	if err := s.PublishAvailability(context.Background(), true); err != nil {
		t.Fatalf("PublishAvailability: %v", err)
	}
	r, _ := rec.Get("trading212/12345678/availability")
	if string(r.Payload) != "online" || !r.Retain {
		t.Errorf("availability = %q retain=%v", r.Payload, r.Retain)
	}
}

// Shutdown must mark the service and every tracked position offline.
func TestPublishOfflineCoversTrackedPositions(t *testing.T) {
	rec := NewRecordingPublisher()
	s := New(rec, haConfig(), trading212.ParseWhitelist("AAPL_US_EQ"))
	ctx := context.Background()

	if err := s.PublishSnapshot(ctx, testSnapshot()); err != nil {
		t.Fatalf("PublishSnapshot: %v", err)
	}
	if err := s.PublishOffline(ctx); err != nil {
		t.Fatalf("PublishOffline: %v", err)
	}

	for _, topic := range []string{
		"trading212/12345678/availability",
		"trading212/12345678/positions/aapl_us_eq/availability",
	} {
		r, ok := rec.Get(topic)
		if !ok {
			t.Fatalf("missing %s", topic)
		}
		if string(r.Payload) != "offline" {
			t.Errorf("%s = %q, want offline", topic, r.Payload)
		}
	}
}

func TestPublishSnapshotPropagatesTransportErrors(t *testing.T) {
	s := New(failingPublisher{}, haConfig(), trading212.ParseWhitelist(""))
	if err := s.PublishSnapshot(context.Background(), testSnapshot()); err == nil {
		t.Fatal("expected the transport error to propagate")
	}
}

// --- helpers ---

func mustGet(t *testing.T, rec *RecordingPublisher, topic string) Record {
	t.Helper()
	r, ok := rec.Get(topic)
	if !ok {
		t.Fatalf("missing topic %s; got %v", topic, rec.Topics())
	}
	return r
}

type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, string, []byte, bool) error {
	return errors.New("transport down")
}

type countingPublisher struct {
	inner *RecordingPublisher
	n     int
}

func (c *countingPublisher) Publish(ctx context.Context, topic string, payload []byte, retain bool) error {
	if strings.HasSuffix(topic, "/config") {
		c.n++
	}
	return c.inner.Publish(ctx, topic, payload, retain)
}

func (c *countingPublisher) discoveryCount() int { return c.n }
