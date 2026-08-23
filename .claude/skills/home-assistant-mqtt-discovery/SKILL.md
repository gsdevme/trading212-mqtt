---
name: home-assistant-mqtt-discovery
description: Use when building or reviewing MQTT payloads and topics for Home Assistant autodiscovery — discovery configs, device blocks, via_device nesting, availability lists, device_class/state_class. Source of truth for internal/homeassistant.
---

# Home Assistant MQTT autodiscovery

Home Assistant creates entities automatically from **retained** MQTT discovery
config messages. These are the rules this project follows.

## Topic structure

Per-entity discovery topic:

    <discovery_prefix>/<component>/<node_id>/<object_id>/config

- `discovery_prefix` — `homeassistant` by default.
- `component` — `sensor`, `binary_sensor`, `device_tracker`, …
- The payload is JSON, published **retained** at **QoS 1**.

Removing an entity means publishing an empty retained payload to its config
topic. This project never does that: entities that stop being relevant flip
their availability instead, which preserves history and avoids discovery churn.

## The `~` abbreviation

Set `"~"` to a device's base topic and reference sub-topics as `"~/state"`,
`"~/availability"`. It keeps payloads small and makes the topic tree obvious.

## Devices and `via_device`

Entities are grouped into a device by a shared `device` block:

    "device": {
      "identifiers": ["t212_12345678"],
      "name": "Trading 212 (12345678)",
      "manufacturer": "Trading 212"
    }

A child device nests under a parent by naming the parent's identifier:

    "device": {
      "identifiers": ["t212_12345678_aapl_us_eq"],
      "name": "Trading 212 – Apple Inc.",
      "manufacturer": "Trading 212",
      "via_device": "t212_12345678"
    }

`via_device` takes an **identifier string** of an already-published device.
Publish the parent's discovery before the children so HA resolves the link
immediately.

## Shared state topic + `value_template`

One retained JSON document per device; each entity reads one field:

    "state_topic": "~/state",
    "value_template": "{{ value_json.free_cash }}"

Entity keys must match the JSON field names exactly. When a template resolves to
`None` (a null in the document), HA renders the entity as unknown — which is the
correct representation of "not computable", e.g. a return percentage with a zero
cost basis.

## Availability

Two forms. A single topic:

    "availability_topic": "~/availability",
    "payload_available": "online",
    "payload_not_available": "offline"

Or a list, when more than one condition must hold:

    "availability": [
      {"topic": "trading212/12345678/availability"},
      {"topic": "~/availability"}
    ],
    "availability_mode": "all"

`availability_mode: all` means every listed topic must report available. This is
how a child device is made unavailable both when the service is down and when
the child itself is not currently applicable.

The service-level topic doubles as the MQTT Last Will and Testament: retained
`offline`, published `online` on connect, retained `offline` again on graceful
shutdown.

## Classes and categories

- `device_class` — semantic type. `monetary` for currency amounts, `timestamp`
  for RFC 3339 times.
- `state_class` — `total` for a value that goes up and down and is meaningful as
  a running figure, `measurement` for an instantaneous reading. A monetary
  balance is `total`; a share price is `measurement`.
- `unit_of_measurement` — for `monetary`, an ISO 4217 code. It must match the
  currency the value is actually denominated in.
- `entity_category: diagnostic` — hides an entity from the main device view.
  Use it for things that matter when debugging but clutter a dashboard.
- `unique_id` — required for HA to allow renaming/customising an entity. Must be
  stable across restarts.

## Republish on reconnect

After an MQTT reconnect, republish discovery and `online`. Retained messages
usually survive a broker restart, but a broker that lost its retained set
otherwise leaves HA with orphaned entities.
