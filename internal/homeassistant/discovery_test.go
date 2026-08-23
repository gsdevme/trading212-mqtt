package homeassistant

import (
	"encoding/json"
	"testing"
)

func testConfig() Config {
	return Config{
		DiscoveryPrefix: "homeassistant",
		TopicPrefix:     "trading212",
		AccountID:       12345678,
		Currency:        "GBP",
	}
}

func TestTopics(t *testing.T) {
	c := testConfig()
	cases := []struct{ got, want string }{
		{c.BaseTopic(), "trading212/12345678"},
		{c.StateTopic(), "trading212/12345678/state"},
		{c.AvailabilityTopic(), "trading212/12345678/availability"},
		{c.PositionBaseTopic("AAPL_US_EQ"), "trading212/12345678/positions/aapl_us_eq"},
		{c.PositionStateTopic("AAPL_US_EQ"), "trading212/12345678/positions/aapl_us_eq/state"},
		{c.PositionAvailabilityTopic("AAPL_US_EQ"), "trading212/12345678/positions/aapl_us_eq/availability"},
		{c.DeviceID(), "t212_12345678"},
		{c.PositionDeviceID("BRK.B_US_EQ"), "t212_12345678_brk_b_us_eq"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func decodePayloads(t *testing.T, msgs []Message) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, m := range msgs {
		var p map[string]any
		if err := json.Unmarshal(m.Payload, &p); err != nil {
			t.Fatalf("unmarshal %s: %v", m.Topic, err)
		}
		out[m.Topic] = p
	}
	return out
}

func TestBuildAccountDiscovery(t *testing.T) {
	msgs, err := BuildAccountDiscovery(testConfig())
	if err != nil {
		t.Fatalf("BuildAccountDiscovery: %v", err)
	}
	if len(msgs) != len(AccountEntities("GBP")) {
		t.Fatalf("got %d messages, want one per entity", len(msgs))
	}

	byTopic := decodePayloads(t, msgs)
	topic := "homeassistant/sensor/t212_12345678/free_cash/config"
	p, ok := byTopic[topic]
	if !ok {
		t.Fatalf("missing discovery topic %s; got %v", topic, msgs)
	}

	if p["~"] != "trading212/12345678" {
		t.Errorf("~ = %v", p["~"])
	}
	if p["state_topic"] != "~/state" {
		t.Errorf("state_topic = %v", p["state_topic"])
	}
	if p["value_template"] != "{{ value_json.free_cash }}" {
		t.Errorf("value_template = %v", p["value_template"])
	}
	if p["unique_id"] != "t212_12345678_free_cash" {
		t.Errorf("unique_id = %v", p["unique_id"])
	}
	if p["device_class"] != "monetary" {
		t.Errorf("device_class = %v", p["device_class"])
	}
	if p["unit_of_measurement"] != "GBP" {
		t.Errorf("unit_of_measurement = %v, want the account currency", p["unit_of_measurement"])
	}

	dev, ok := p["device"].(map[string]any)
	if !ok {
		t.Fatal("missing device block")
	}
	ids, _ := dev["identifiers"].([]any)
	if len(ids) != 1 || ids[0] != "t212_12345678" {
		t.Errorf("device identifiers = %v", dev["identifiers"])
	}
	if dev["manufacturer"] != "Trading 212" {
		t.Errorf("manufacturer = %v", dev["manufacturer"])
	}
	if _, hasVia := dev["via_device"]; hasVia {
		t.Error("the account device must not have a via_device")
	}

	avail, ok := p["availability"].([]any)
	if !ok || len(avail) != 1 {
		t.Fatalf("availability = %v, want a single-entry list", p["availability"])
	}
	first, _ := avail[0].(map[string]any)
	if first["topic"] != "trading212/12345678/availability" {
		t.Errorf("availability topic = %v", first["topic"])
	}
}

func TestAccountDiagnosticCategories(t *testing.T) {
	msgs, err := BuildAccountDiscovery(testConfig())
	if err != nil {
		t.Fatalf("BuildAccountDiscovery: %v", err)
	}
	byTopic := decodePayloads(t, msgs)

	diagnostic := []string{"realized_pl", "cash_in_pies", "cash_reserved", "last_updated"}
	for _, key := range diagnostic {
		p := byTopic["homeassistant/sensor/t212_12345678/"+key+"/config"]
		if p == nil {
			t.Fatalf("missing entity %s", key)
		}
		if p["entity_category"] != "diagnostic" {
			t.Errorf("%s entity_category = %v, want diagnostic", key, p["entity_category"])
		}
	}

	primary := []string{"total_value", "free_cash", "invested", "current_value", "unrealized_pl", "return_pct"}
	for _, key := range primary {
		p := byTopic["homeassistant/sensor/t212_12345678/"+key+"/config"]
		if p == nil {
			t.Fatalf("missing entity %s", key)
		}
		if _, has := p["entity_category"]; has {
			t.Errorf("%s must not be diagnostic", key)
		}
	}
}

func TestBuildPositionDiscoveryNestsViaDevice(t *testing.T) {
	msgs, err := BuildPositionDiscovery(testConfig(), "AAPL_US_EQ", "Apple Inc.", "USD")
	if err != nil {
		t.Fatalf("BuildPositionDiscovery: %v", err)
	}
	byTopic := decodePayloads(t, msgs)

	p := byTopic["homeassistant/sensor/t212_12345678_aapl_us_eq/quantity/config"]
	if p == nil {
		t.Fatalf("missing quantity entity; topics: %v", msgs)
	}

	dev, _ := p["device"].(map[string]any)
	if dev["via_device"] != "t212_12345678" {
		t.Errorf("via_device = %v, want the account device id", dev["via_device"])
	}
	if dev["name"] != "Trading 212 – Apple Inc." {
		t.Errorf("device name = %v", dev["name"])
	}
	ids, _ := dev["identifiers"].([]any)
	if len(ids) != 1 || ids[0] != "t212_12345678_aapl_us_eq" {
		t.Errorf("identifiers = %v", dev["identifiers"])
	}
	if p["~"] != "trading212/12345678/positions/aapl_us_eq" {
		t.Errorf("~ = %v", p["~"])
	}
}

// The rule most easily broken, asserted at the payload level.
func TestPositionDiscoveryTwoCurrencyRule(t *testing.T) {
	msgs, err := BuildPositionDiscovery(testConfig(), "AAPL_US_EQ", "Apple Inc.", "USD")
	if err != nil {
		t.Fatalf("BuildPositionDiscovery: %v", err)
	}
	byTopic := decodePayloads(t, msgs)

	base := "homeassistant/sensor/t212_12345678_aapl_us_eq/"
	instrumentPriced := []string{"avg_price", "current_price"}
	accountPriced := []string{"value", "cost", "unrealized_pl", "fx_impact"}

	for _, key := range instrumentPriced {
		p := byTopic[base+key+"/config"]
		if p == nil {
			t.Fatalf("missing entity %s", key)
		}
		if p["unit_of_measurement"] != "USD" {
			t.Errorf("%s unit = %v, want USD (instrument currency)", key, p["unit_of_measurement"])
		}
	}
	for _, key := range accountPriced {
		p := byTopic[base+key+"/config"]
		if p == nil {
			t.Fatalf("missing entity %s", key)
		}
		if p["unit_of_measurement"] != "GBP" {
			t.Errorf("%s unit = %v, want GBP (account currency)", key, p["unit_of_measurement"])
		}
	}
}

func TestPositionEntitiesListBothAvailabilityTopics(t *testing.T) {
	msgs, err := BuildPositionDiscovery(testConfig(), "AAPL_US_EQ", "Apple Inc.", "USD")
	if err != nil {
		t.Fatalf("BuildPositionDiscovery: %v", err)
	}
	byTopic := decodePayloads(t, msgs)
	p := byTopic["homeassistant/sensor/t212_12345678_aapl_us_eq/value/config"]

	if p["availability_mode"] != "all" {
		t.Errorf("availability_mode = %v, want all", p["availability_mode"])
	}
	avail, _ := p["availability"].([]any)
	if len(avail) != 2 {
		t.Fatalf("availability = %v, want two entries", p["availability"])
	}
	svc, _ := avail[0].(map[string]any)
	pos, _ := avail[1].(map[string]any)
	if svc["topic"] != "trading212/12345678/availability" {
		t.Errorf("first availability topic = %v, want the service topic", svc["topic"])
	}
	if pos["topic"] != "trading212/12345678/positions/aapl_us_eq/availability" {
		t.Errorf("second availability topic = %v, want the position topic", pos["topic"])
	}
}

func TestTimestampEntitiesHaveNoUnit(t *testing.T) {
	msgs, err := BuildAccountDiscovery(testConfig())
	if err != nil {
		t.Fatalf("BuildAccountDiscovery: %v", err)
	}
	p := decodePayloads(t, msgs)["homeassistant/sensor/t212_12345678/last_updated/config"]
	if p["device_class"] != "timestamp" {
		t.Errorf("device_class = %v", p["device_class"])
	}
	if _, has := p["unit_of_measurement"]; has {
		t.Error("a timestamp entity must not carry a unit")
	}
	if _, has := p["state_class"]; has {
		t.Error("a timestamp entity must not carry a state_class")
	}
}
