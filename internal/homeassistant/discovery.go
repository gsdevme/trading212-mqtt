package homeassistant

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/gsdevme/trading212-mqtt/internal/trading212"
)

// manufacturer is the device manufacturer shown in Home Assistant.
const manufacturer = "Trading 212"

// Config identifies the topics and device metadata for one account's discovery.
type Config struct {
	DiscoveryPrefix string // e.g. "homeassistant"
	TopicPrefix     string // e.g. "trading212"
	AccountID       int64
	Currency        string // account primary currency, ISO 4217
}

// Message is a single MQTT publish. Discovery messages are always retained at
// QoS 1.
type Message struct {
	Topic   string
	Payload []byte
}

// BaseTopic is the account's base topic, used as the `~` abbreviation.
func (c Config) BaseTopic() string {
	return c.TopicPrefix + "/" + strconv.FormatInt(c.AccountID, 10)
}

// StateTopic carries the retained account state document.
func (c Config) StateTopic() string { return c.BaseTopic() + "/state" }

// AvailabilityTopic is the service-level LWT/availability topic.
func (c Config) AvailabilityTopic() string { return c.BaseTopic() + "/availability" }

// PositionBaseTopic is one position's base topic, used as its `~`.
func (c Config) PositionBaseTopic(ticker string) string {
	return c.BaseTopic() + "/positions/" + trading212.Slug(ticker)
}

// PositionStateTopic carries a position's retained state document.
func (c Config) PositionStateTopic(ticker string) string {
	return c.PositionBaseTopic(ticker) + "/state"
}

// PositionAvailabilityTopic reports whether a tracked ticker is currently held.
func (c Config) PositionAvailabilityTopic(ticker string) string {
	return c.PositionBaseTopic(ticker) + "/availability"
}

// DeviceID is the account device's Home Assistant identifier.
func (c Config) DeviceID() string {
	return "t212_" + strconv.FormatInt(c.AccountID, 10)
}

// PositionDeviceID is a position device's identifier, nested under DeviceID via
// via_device.
func (c Config) PositionDeviceID(ticker string) string {
	return c.DeviceID() + "_" + trading212.Slug(ticker)
}

// BuildAccountDiscovery returns the retained discovery config messages for every
// account entity.
func BuildAccountDiscovery(c Config) ([]Message, error) {
	device := map[string]any{
		"identifiers":  []string{c.DeviceID()},
		"manufacturer": manufacturer,
		"name":         fmt.Sprintf("Trading 212 (%d)", c.AccountID),
	}
	availability := []map[string]string{{"topic": c.AvailabilityTopic()}}

	entities := AccountEntities(c.Currency)
	msgs := make([]Message, 0, len(entities))
	for _, e := range entities {
		p := entityPayload(e, c.BaseTopic(), c.DeviceID(), device, availability)
		body, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("marshal account discovery for %s: %w", e.Key, err)
		}
		msgs = append(msgs, Message{
			Topic:   fmt.Sprintf("%s/%s/%s/%s/config", c.DiscoveryPrefix, e.Component, c.DeviceID(), e.Key),
			Payload: body,
		})
	}
	return msgs, nil
}

// BuildPositionDiscovery returns the retained discovery config messages for one
// position device.
//
// name and instrumentCurrency are plain arguments rather than a Position so the
// publisher can emit placeholder discovery for a whitelisted ticker it has not
// yet seen, then republish with real metadata on first sighting.
func BuildPositionDiscovery(c Config, ticker, name, instrumentCurrency string) ([]Message, error) {
	if name == "" {
		name = ticker
	}
	deviceID := c.PositionDeviceID(ticker)
	device := map[string]any{
		"identifiers":  []string{deviceID},
		"manufacturer": manufacturer,
		"name":         "Trading 212 – " + name,
		"via_device":   c.DeviceID(),
	}
	// Both conditions must hold: the service must be up AND the position must be
	// currently held.
	availability := []map[string]string{
		{"topic": c.AvailabilityTopic()},
		{"topic": c.PositionAvailabilityTopic(ticker)},
	}

	entities := PositionEntities(c.Currency, instrumentCurrency)
	msgs := make([]Message, 0, len(entities))
	for _, e := range entities {
		p := entityPayload(e, c.PositionBaseTopic(ticker), deviceID, device, availability)
		body, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("marshal position discovery for %s/%s: %w", ticker, e.Key, err)
		}
		msgs = append(msgs, Message{
			Topic:   fmt.Sprintf("%s/%s/%s/%s/config", c.DiscoveryPrefix, e.Component, deviceID, e.Key),
			Payload: body,
		})
	}
	return msgs, nil
}

// entityPayload builds one discovery config. Optional keys are omitted rather
// than emitted empty, because Home Assistant rejects some empty values outright.
func entityPayload(e Entity, baseTopic, deviceID string, device map[string]any, availability []map[string]string) map[string]any {
	uniq := deviceID + "_" + e.Key
	p := map[string]any{
		"~":                     baseTopic,
		"name":                  e.Name,
		"unique_id":             uniq,
		"object_id":             uniq,
		"state_topic":           "~/state",
		"value_template":        fmt.Sprintf("{{ value_json.%s }}", e.Key),
		"availability":          availability,
		"availability_mode":     "all",
		"payload_available":     "online",
		"payload_not_available": "offline",
		"device":                device,
	}
	if e.DeviceClass != "" {
		p["device_class"] = e.DeviceClass
	}
	if e.StateClass != "" {
		p["state_class"] = e.StateClass
	}
	if e.Unit != "" {
		p["unit_of_measurement"] = e.Unit
	}
	if e.Category != "" {
		p["entity_category"] = e.Category
	}
	return p
}
