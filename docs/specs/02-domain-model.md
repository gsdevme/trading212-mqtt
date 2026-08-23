# 02 — Domain model

`internal/trading212` defines the domain types the rest of the service works with.
Unexported wire DTOs (matching the raw JSON shapes in `01-trading212-api.md`) are
parsed and mapped into these exported types; nothing downstream of `internal/trading212`
ever sees a wire DTO (`REQ-DM-08`). The domain types' JSON tags are also the field
names of the published MQTT state document — the same struct that gets mapped from
the wire response is marshalled straight back out onto the state topic, so entity
`value_template` expressions read these tag names directly.

## `AccountSummary`

| Go field | JSON tag | Type | Source |
|---|---|---|---|
| `ID` | `id` | `int64` | `summary.id` |
| `Currency` | `currency` | `string` | `summary.currency` |
| `TotalValue` | `total_value` | `float64` | `summary.totalValue` |
| `FreeCash` | `free_cash` | `float64` | `summary.cash.availableToTrade` |
| `CashInPies` | `cash_in_pies` | `float64` | `summary.cash.inPies` |
| `CashReserved` | `cash_reserved` | `float64` | `summary.cash.reservedForOrders` |
| `Invested` | `invested` | `float64` | `summary.investments.totalCost` |
| `CurrentValue` | `current_value` | `float64` | `summary.investments.currentValue` |
| `UnrealizedPL` | `unrealized_pl` | `float64` | `summary.investments.unrealizedProfitLoss` |
| `RealizedPL` | `realized_pl` | `float64` | `summary.investments.realizedProfitLoss` |
| `ReturnPct` | `return_pct,omitempty` | `*float64` | derived |
| `LastUpdated` | `last_updated` | `time.Time` | poll completion time |

This covers `id`, `currency`, the total value, the three cash figures, the four
investment figures, the derived `ReturnPct` and `LastUpdated` (`REQ-DM-01`).

## `Position`

| Go field | JSON tag | Type | Source |
|---|---|---|---|
| `Ticker` | `ticker` | `string` | `instrument.ticker` |
| `Slug` | `slug` | `string` | derived from `Ticker` |
| `Name` | `name` | `string` | `instrument.name` |
| `ISIN` | `isin` | `string` | `instrument.isin` |
| `InstrumentCurrency` | `instrument_currency` | `string` | `instrument.currency` |
| `AccountCurrency` | `account_currency` | `string` | `walletImpact.currency` |
| `Quantity` | `quantity` | `float64` | `quantity` |
| `QuantityAvailable` | `quantity_available` | `float64` | `quantityAvailableForTrading` |
| `QuantityInPies` | `quantity_in_pies` | `float64` | `quantityInPies` |
| `AvgPrice` | `avg_price` | `float64` | `averagePricePaid` (instrument currency) |
| `CurrentPrice` | `current_price` | `float64` | `currentPrice` (instrument currency) |
| `Value` | `value` | `float64` | `walletImpact.currentValue` (account currency) |
| `Cost` | `cost` | `float64` | `walletImpact.totalCost` (account currency) |
| `UnrealizedPL` | `unrealized_pl` | `float64` | `walletImpact.unrealizedProfitLoss` (account currency) |
| `FXImpact` | `fx_impact` | `float64` | `walletImpact.fxImpact` (account currency) |
| `ReturnPct` | `return_pct,omitempty` | `*float64` | derived |
| `Opened` | `opened` | `time.Time` | `createdAt` |

This covers instrument identity (`Ticker`, `Slug`, `Name`, `ISIN`), both currencies
(`InstrumentCurrency`, `AccountCurrency`), quantities (`Quantity`,
`QuantityAvailable`, `QuantityInPies`), both prices (`AvgPrice`, `CurrentPrice`), the
four wallet-impact figures (`Value`, `Cost`, `UnrealizedPL`, `FXImpact`), and the
derived `ReturnPct` and `Opened` (`REQ-DM-02`).

Price fields (`AvgPrice`, `CurrentPrice`) are denominated in `InstrumentCurrency`;
wallet-impact fields (`Value`, `Cost`, `UnrealizedPL`, `FXImpact`) are denominated in
`AccountCurrency` (`REQ-DM-05`) — see the two-currency rule in
`01-trading212-api.md`.

## `Snapshot`

| Go field | JSON tag | Type | Meaning |
|---|---|---|---|
| `Account` | `account` | `AccountSummary` | Result of the poll's summary call |
| `Positions` | `positions` | `[]Position` | Result of the poll's positions call, unfiltered |

One `Snapshot` is produced per successful poll (`REQ-SC-02`). Whitelist filtering
happens downstream, in the publisher, not inside the `Snapshot` itself — the
`Snapshot` is a faithful, complete record of what the two API calls returned.

## The optional-field convention

Any field that can legitimately be absent, not-yet-computable or not-yet-known is a
**pointer**, never a zero value standing in for "unknown". A pointer is `nil` when
the value is absent; a non-nil pointer, even to `0`, means the value is genuinely
zero. This keeps "we don't know" distinguishable from "we know it's zero" all the way
out to the MQTT payload and into Home Assistant, where a `nil` renders the entity as
`unknown` rather than a misleading `0` (`REQ-DM-03`).

`ReturnPct` on both `AccountSummary` and `Position` is the only field family this
applies to today.

## The two derived fields and their zero-cost guard

Both `AccountSummary.ReturnPct` and `Position.ReturnPct` are computed, not read
directly off the wire:

- `AccountSummary.ReturnPct` = `investments.unrealizedProfitLoss / investments.totalCost * 100`
- `Position.ReturnPct` = `walletImpact.unrealizedProfitLoss / walletImpact.totalCost * 100`

Both are guarded against division by zero: when the relevant cost figure is exactly
`0`, the field is left `nil` rather than computed as `+Inf`, `-Inf` or `NaN`
(`REQ-DM-04`). A freshly opened position with a `0` cost basis — or any position
priced at exactly its cost — is the concrete case this guard exists for; it is one of
the required test fixtures (see `07-testing.md`).

## The `Slug` normalisation rule

`Slug` lower-cases a ticker and replaces every character outside `[a-z0-9_]` with
`_` (`REQ-DM-06`). It exists because a raw ticker like `AAPL_US_EQ` is not guaranteed
to satisfy every consumer's character constraints on its own — MQTT topic segments
and Home Assistant's `object_id` both restrict the character set more tightly than
"whatever Trading 212 happens to name an instrument" — so the slug is what actually
appears in MQTT topic paths and in HA `unique_id`/`object_id` values, never the raw
ticker.

**Slug is the tracking identity.** The publisher tracks each position by its `Slug`,
not by its raw ticker string. A whitelist entry and the API's `instrument.ticker` for
the same holding can differ only in case (`AAPL_US_EQ` vs `aapl_us_eq`); tracked by
raw ticker these would become two distinct bookkeeping entries that happen to slug to
the same MQTT topic, so one entry's "not held" sweep could publish a retained
`offline` to a topic the other entry's "held" branch just published `online` to in
the same poll — visible entity flapping in Home Assistant. Since the slug is already
the topic identity, tracking by slug keeps the bookkeeping and the wire in agreement
by construction. See `03-mqtt-ha-discovery.md` for how this plays out across a poll.

## `Whitelist` semantics

`Whitelist` is parsed from the `TICKERS` environment variable (see `05-config.md`)
and has three states:

- **Empty** — tracks nothing. No position devices are created; only the account
  device is published.
- **`*`** — tracks whatever is held. Devices are discovered from poll results: a
  position opened while the service is running gets discovery published at the poll
  that first sees it.
- **A comma-separated list** (e.g. `AAPL_US_EQ,VUSA_EQ`) — tracks exactly those
  tickers. Devices for every listed ticker are created at startup, whether or not the
  position is currently held (`REQ-DM-07`).

**Casing.** Matching against the API's `instrument.ticker` is case-insensitive, but
the ticker strings stored in a parsed `Whitelist` preserve exactly the casing the user
typed in `TICKERS`. The reason is naming, not matching: a position device for a
ticker that is whitelisted but not yet held has nothing from the API to name itself
after, so it is named after the configured ticker string — and that string must
survive parsing unchanged, in the casing the user chose, rather than being
normalised to upper- or lower-case before anyone has a chance to see it in Home
Assistant. Matching a poll's `instrument.ticker` values against the whitelist folds
case on both sides; naming a placeholder device uses the whitelist's original casing.
