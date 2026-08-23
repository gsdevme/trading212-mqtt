# 03 — MQTT & Home Assistant discovery

## Topic shapes

```
trading212/<account_id>/state                              retained JSON, account totals
trading212/<account_id>/availability                        retained online|offline (LWT)
trading212/<account_id>/positions/<slug>/state              retained JSON, one position
trading212/<account_id>/positions/<slug>/availability       retained online|offline
```

`<slug>` is `Position.Slug` (see `02-domain-model.md`), never the raw ticker — a
whitelist entry and the API's ticker for the same holding can differ only in case, and
tracking/publishing by slug is what keeps two casings of the same ticker resolving to
one topic instead of two (`REQ-DM-06`).

One state topic per device, rather than a single account-wide document with positions
nested inside it. Each discovery config points at its own topic via a `~` base-topic
abbreviation, keeping `value_template` expressions one level deep, and — more
importantly — giving every position device an availability topic of its own
(`REQ-HA-02`).

All discovery, state and availability publishes are **retained** at **QoS 1**
(`REQ-HA-11`).

## Discovery topics

Per-entity discovery topic:

```
<discovery_prefix>/<component>/<node_id>/<object_id>/config
```

retained, QoS 1, published once at startup and again after every MQTT (re)connection
(`REQ-HA-01`, `REQ-HA-13`).

## Devices

**Account device.**

```json
"device": {
  "identifiers": ["t212_<account_id>"],
  "name": "Trading 212 (<account_id>)",
  "manufacturer": "Trading 212"
}
```

**Position devices.** One per tracked ticker, keyed by slug:

```json
"device": {
  "identifiers": ["t212_<account_id>_<slug>"],
  "name": "Trading 212 – <instrument.name>",
  "manufacturer": "Trading 212",
  "via_device": "t212_<account_id>"
}
```

`via_device` nests the position device under the account device in Home Assistant's
device registry (`REQ-HA-03`). The account device's discovery must be published before
any position device's so HA can resolve the link immediately.

## Account entity catalogue

| Object id | Source field | Device class | State class | Category |
|---|---|---|---|---|
| `total_value` | `totalValue` | monetary | total | — |
| `free_cash` | `cash.availableToTrade` | monetary | total | — |
| `invested` | `investments.totalCost` | monetary | total | — |
| `current_value` | `investments.currentValue` | monetary | total | — |
| `unrealized_pl` | `investments.unrealizedProfitLoss` | monetary | total | — |
| `return_pct` | derived (see below) | — (`%`) | measurement | — |
| `realized_pl` | `investments.realizedProfitLoss` | monetary | total | diagnostic |
| `cash_in_pies` | `cash.inPies` | monetary | total | diagnostic |
| `cash_reserved` | `cash.reservedForOrders` | monetary | total | diagnostic |
| `last_updated` | poll completion time | timestamp | — | diagnostic |

Monetary entities use the account primary currency as `unit_of_measurement`
(`REQ-HA-04`).

`return_pct` is derived as `unrealizedProfitLoss / totalCost × 100`, and is `nil` when
`totalCost` is zero.

## Position entity catalogue

| Object id | Source field | Currency | Device class | State class | Category |
|---|---|---|---|---|---|
| `quantity` | `quantity` | — | — | measurement | — |
| `avg_price` | `averagePricePaid` | instrument | monetary | measurement | — |
| `current_price` | `currentPrice` | instrument | monetary | measurement | — |
| `value` | `walletImpact.currentValue` | account | monetary | total | — |
| `unrealized_pl` | `walletImpact.unrealizedProfitLoss` | account | monetary | total | — |
| `return_pct` | derived | — (`%`) | — | measurement | — |
| `cost` | `walletImpact.totalCost` | account | monetary | total | diagnostic |
| `fx_impact` | `walletImpact.fxImpact` | account | monetary | total | diagnostic |
| `quantity_in_pies` | `quantityInPies` | — | — | measurement | diagnostic |
| `opened` | `createdAt` | — | timestamp | — | diagnostic |

`avg_price` and `current_price` use the instrument currency as `unit_of_measurement`;
every other monetary entity in this table uses the account currency (`REQ-HA-05`).

Position `return_pct` is `walletImpact.unrealizedProfitLoss / walletImpact.totalCost ×
100`, `nil` when `totalCost` is zero.

## Diagnostic entities

`entity_category: diagnostic` is set on: realized P/L, both non-tradeable cash figures
(`cash_in_pies`, `cash_reserved`), `last_updated`, `cost`, `fx_impact`,
`quantity_in_pies` and `opened` (`REQ-HA-10`). These are the entities that matter when
debugging or auditing but would clutter the main device card.

## Availability

Every entity lists **two** availability topics with `availability_mode: all`
(`REQ-HA-07`):

1. The service-level topic, `trading212/<account_id>/availability`, registered as the
   MQTT Last Will and Testament with a retained `offline` payload, published `online`
   on connect and retained `offline` again on graceful shutdown (`REQ-HA-06`).
2. For position entities only, the position-level topic
   `trading212/<account_id>/positions/<slug>/availability`.

`availability_mode: all` means every listed topic must report available for HA to show
the entity as available — a position device goes unavailable both when the service
itself is down and when that specific position is not currently applicable, without
needing two separate code paths downstream in HA.

## Position lifecycle across a poll

The publisher tracks positions by `Slug`, not by raw ticker (`02-domain-model.md`).
Each poll, for every slug the publisher already knows about or that this poll's
(whitelist-filtered) results introduce:

- **Held this poll, not previously known** (only relevant when `TICKERS=*`) — publish
  discovery for the new position device, then its state, then flip its availability to
  `online`.
- **Held this poll, already known** — publish updated state; leave availability
  `online`.
- **Not held this poll, previously held** — leave the retained state topic untouched
  (last known values persist) and publish retained `offline` to the position's
  availability topic (`REQ-HA-08`).
- **Statically whitelisted, never yet seen held** — see placeholder discovery, below.

Because tracking keys on the slug and the slug is also the topic path component, a
whitelist entry and an API ticker that differ only in case resolve to the *same*
tracked entry and the *same* topic — the poll can only ever decide `online` or
`offline` once per slug, never both, which is what prevents the flapping this design
guards against.

## Placeholder discovery for not-yet-held tickers

A statically whitelisted ticker (an explicit entry in a comma-separated `TICKERS`
list) gets its discovery configs published at **startup**, before any poll has run,
using the whitelist's own casing for the device name and the account's primary
currency as a stand-in for the instrument currency (there is no instrument metadata
yet) (`REQ-HA-09`). Its availability starts `offline` — the position is not held —
and its state topic carries no retained payload until the position first appears.

The first poll that observes the ticker held republishes discovery with the real
instrument metadata (name, ISIN, instrument currency) now known from the API, so the
device's currency and display name correct themselves in place without any topic
change and without deleting or re-adding the device. Selling out of a holding and
buying back into it later needs no discovery churn at all — only the availability
payload flips between `online` and `offline` (`REQ-HA-08`, `REQ-HA-09`).

## Publisher abstraction

`Publisher` is an interface with an autopaho-backed implementation for real MQTT
brokers and a recording fake for tests that asserts on published topics and payloads
without a broker (`REQ-HA-12`).
