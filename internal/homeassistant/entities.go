// Package homeassistant builds Home Assistant MQTT autodiscovery payloads. See
// docs/specs/03-mqtt-ha-discovery.md and the home-assistant-mqtt-discovery
// skill, which are the source of truth for these rules.
package homeassistant

// Component types.
const (
	Sensor = "sensor"
)

// Entity describes a single Home Assistant entity derived from a device's shared
// JSON state topic. Key is both the discovery object-id and the JSON field the
// entity reads (value_json.<Key>), so entity keys must match the domain types'
// JSON tags exactly.
type Entity struct {
	Component   string
	Key         string
	Name        string
	DeviceClass string
	StateClass  string
	Unit        string
	Category    string // "diagnostic" or empty

	// Precision is the suggested_display_precision Home Assistant uses when
	// rendering the entity, or nil to let HA choose. A pointer because zero
	// decimals is a legitimate value, not "unset".
	Precision *int
}

// precision is a helper for the Entity.Precision literal, which needs an
// addressable int.
func precision(n int) *int { return &n }

// AccountEntities returns the catalogue published for the account device. Every
// monetary entity is denominated in currency, the account's primary currency.
//
// Balances use state_class "total" because they rise and fall and are meaningful
// as a running figure; the return percentage is an instantaneous "measurement".
func AccountEntities(currency string) []Entity {
	return []Entity{
		{Component: Sensor, Key: "total_value", Name: "Total value", DeviceClass: "monetary", StateClass: "total", Unit: currency},
		{Component: Sensor, Key: "free_cash", Name: "Free cash", DeviceClass: "monetary", StateClass: "total", Unit: currency},
		{Component: Sensor, Key: "invested", Name: "Invested", DeviceClass: "monetary", StateClass: "total", Unit: currency},
		{Component: Sensor, Key: "current_value", Name: "Investments value", DeviceClass: "monetary", StateClass: "total", Unit: currency},
		{Component: Sensor, Key: "unrealized_pl", Name: "Unrealised P/L", DeviceClass: "monetary", StateClass: "total", Unit: currency},
		{Component: Sensor, Key: "return_pct", Name: "Return", StateClass: "measurement", Unit: "%", Precision: precision(2)},

		// Diagnostics: useful when reconciling, noise on a dashboard.
		{Component: Sensor, Key: "realized_pl", Name: "Realised P/L", DeviceClass: "monetary", StateClass: "total", Unit: currency, Category: "diagnostic"},
		{Component: Sensor, Key: "cash_in_pies", Name: "Cash in pies", DeviceClass: "monetary", StateClass: "total", Unit: currency, Category: "diagnostic"},
		{Component: Sensor, Key: "cash_reserved", Name: "Cash reserved for orders", DeviceClass: "monetary", StateClass: "total", Unit: currency, Category: "diagnostic"},
		{Component: Sensor, Key: "last_updated", Name: "Last updated", DeviceClass: "timestamp", Category: "diagnostic"},
	}
}

// PositionEntities returns the catalogue published for one position device.
//
// The two currencies are deliberately distinct: avg_price and current_price are
// denominated in instrumentCurrency, while every wallet-impact figure is
// denominated in accountCurrency. See docs/specs/01-trading212-api.md.
func PositionEntities(accountCurrency, instrumentCurrency string) []Entity {
	if instrumentCurrency == "" {
		instrumentCurrency = accountCurrency
	}
	return []Entity{
		{Component: Sensor, Key: "quantity", Name: "Quantity", StateClass: "measurement"},
		{Component: Sensor, Key: "avg_price", Name: "Average price", DeviceClass: "monetary", StateClass: "measurement", Unit: instrumentCurrency},
		{Component: Sensor, Key: "current_price", Name: "Current price", DeviceClass: "monetary", StateClass: "measurement", Unit: instrumentCurrency},
		{Component: Sensor, Key: "value", Name: "Value", DeviceClass: "monetary", StateClass: "total", Unit: accountCurrency},
		{Component: Sensor, Key: "unrealized_pl", Name: "Unrealised P/L", DeviceClass: "monetary", StateClass: "total", Unit: accountCurrency},
		{Component: Sensor, Key: "return_pct", Name: "Return", StateClass: "measurement", Unit: "%", Precision: precision(2)},

		// Diagnostics.
		{Component: Sensor, Key: "cost", Name: "Cost basis", DeviceClass: "monetary", StateClass: "total", Unit: accountCurrency, Category: "diagnostic"},
		{Component: Sensor, Key: "fx_impact", Name: "FX impact", DeviceClass: "monetary", StateClass: "total", Unit: accountCurrency, Category: "diagnostic"},
		{Component: Sensor, Key: "quantity_in_pies", Name: "Quantity in pies", StateClass: "measurement", Category: "diagnostic"},
		{Component: Sensor, Key: "opened", Name: "Opened", DeviceClass: "timestamp", Category: "diagnostic"},
	}
}
