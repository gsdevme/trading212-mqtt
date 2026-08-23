# 01 — Trading 212 API

This is the **normative** description of the Trading 212 Public API surface this
project uses. `.claude/skills/trading212-api/SKILL.md` covers the same ground as a
working aid for whoever is writing code against it; the two must agree, and this
document wins if they ever drift.

Reference: <https://docs.trading212.com/api>. The API is in **beta** and is enabled
only for **Invest and Stocks ISA** account types.

## Environments

| Mode | Base URL |
|---|---|
| Live | `https://live.trading212.com/api/v0` |
| Demo (paper trading) | `https://demo.trading212.com/api/v0` |

Both base URLs are defined as constants (`REQ-T2-01`); `MODE` selects between them (see
`05-config.md`).

## Authentication

HTTP Basic, API key as username and API secret as password:

    Authorization: Basic base64(API_KEY + ":" + API_SECRET)

A legacy scheme sending the bare API key as the whole `Authorization` header value is
also accepted upstream. This project falls back to it whenever `T212_API_SECRET` is
empty — the bare key is sent as the entire header value, not wrapped in `Basic
base64(...)` (`REQ-T2-02`). Neither the key nor the secret is ever logged.

## Rate limiting

Limits are enforced **per account** — not per key, not per IP. A second consumer of
the same account (a human using the app, another integration) shares the same budget,
which is why this project rate-limits itself defensively rather than assuming it is
the only caller.

Every response carries:

| Header | Meaning |
|---|---|
| `x-ratelimit-limit` | Requests allowed in the current period |
| `x-ratelimit-period` | Period length in seconds |
| `x-ratelimit-remaining` | Requests left in the period |
| `x-ratelimit-reset` | Unix timestamp when the limit fully resets |
| `x-ratelimit-used` | Requests already made in the period |

The limiter permits bursts within a period rather than enforcing strict spacing on its
own account; this project's own limiter additionally enforces a per-endpoint minimum
spacing on top of what the upstream API allows (see `04-polling-scheduling.md`). On a
`429`, the correct behaviour is to wait until `x-ratelimit-reset` rather than guessing
a backoff duration.

## Endpoints consumed

Only these two. This service is **strictly read-only**: no order, cancellation or pie
endpoint may be called anywhere in the tree (`REQ-T2-05`).

| Endpoint | Limit | Purpose |
|---|---|---|
| `GET /api/v0/equity/account/summary` | 1 req / 5s | Account totals, id, primary currency |
| `GET /api/v0/equity/positions` | 1 req / 1s | All open positions |

`GET /equity/positions` accepts an optional `ticker` query parameter that filters
server-side. This project does not use it — see `04-polling-scheduling.md` for why
(`REQ-T2-04`).

### `GET /equity/account/summary` — full response schema

```json
{
  "id": 12345678,
  "currency": "GBP",
  "totalValue": 0.0,
  "cash": { "availableToTrade": 0.0, "inPies": 0.0, "reservedForOrders": 0.0 },
  "investments": {
    "currentValue": 0.0,
    "totalCost": 0.0,
    "unrealizedProfitLoss": 0.0,
    "realizedProfitLoss": 0.0
  }
}
```

| Field | Type | Meaning |
|---|---|---|
| `id` | integer | Account number, as shown in the Trading 212 apps |
| `currency` | string (ISO 4217) | The account's primary currency |
| `totalValue` | number | Total account value in `currency` |
| `cash.availableToTrade` | number | Free cash |
| `cash.inPies` | number | Cash allocated to Pies |
| `cash.reservedForOrders` | number | Cash reserved by open orders |
| `investments.currentValue` | number | Current market value of all holdings |
| `investments.totalCost` | number | Total cost basis of all holdings |
| `investments.unrealizedProfitLoss` | number | Unrealized P/L across all holdings |
| `investments.realizedProfitLoss` | number | Realized P/L across all holdings |

### `GET /equity/positions` — full response schema

Returns a JSON array; each element:

```json
{
  "instrument": {
    "ticker": "AAPL_US_EQ",
    "name": "Apple Inc.",
    "isin": "US0378331005",
    "currency": "USD"
  },
  "quantity": 0.0,
  "quantityAvailableForTrading": 0.0,
  "quantityInPies": 0.0,
  "averagePricePaid": 0.0,
  "currentPrice": 0.0,
  "createdAt": "2024-01-01T00:00:00Z",
  "walletImpact": {
    "currency": "GBP",
    "currentValue": 0.0,
    "totalCost": 0.0,
    "unrealizedProfitLoss": 0.0,
    "fxImpact": 0.0
  }
}
```

| Field | Type | Meaning |
|---|---|---|
| `instrument.ticker` | string | Trading 212 instrument identifier, e.g. `AAPL_US_EQ` |
| `instrument.name` | string | Human-readable instrument name |
| `instrument.isin` | string | ISIN |
| `instrument.currency` | string (ISO 4217) | Currency the instrument itself is priced in |
| `quantity` | number | Shares held |
| `quantityAvailableForTrading` | number | Shares not locked by pending orders |
| `quantityInPies` | number | Shares held inside a Pie |
| `averagePricePaid` | number | Average price paid per share, in `instrument.currency` |
| `currentPrice` | number | Current market price per share, in `instrument.currency` |
| `createdAt` | RFC 3339 timestamp | When the position was first opened |
| `walletImpact.currency` | string (ISO 4217) | Always equal to `summary.currency` |
| `walletImpact.currentValue` | number | Current value in the account currency |
| `walletImpact.totalCost` | number | Cost basis in the account currency |
| `walletImpact.unrealizedProfitLoss` | number | Unrealized P/L in the account currency |
| `walletImpact.fxImpact` | number | Portion of P/L attributable to FX movement, in the account currency |

## The two-currency rule

**`averagePricePaid` and `currentPrice` are denominated in `instrument.currency`.
Every field under `walletImpact` is denominated in the account's primary currency
(`walletImpact.currency`, which always equals `summary.currency`).**

These two currencies are frequently different, and nothing in the response shape
prevents conflating them — both are plain JSON numbers with no embedded unit. Getting
this wrong silently misreports a position rather than failing loudly, which is worse,
so every entity built from these fields must carry the `unit_of_measurement` that
matches the field it was built from, not a single currency assumed for the whole
position.

**Worked example.** A GBP account (`summary.currency = "GBP"`) holds `AAPL_US_EQ`
(`instrument.currency = "USD"`). A response might read:

- `averagePricePaid: 150.00` — 150.00 **USD** per share, the price actually paid.
- `currentPrice: 175.32` — 175.32 **USD** per share, the current market price.
- `walletImpact.currentValue: 1402.50` — 1402.50 **GBP**, the position's current worth
  converted into the account currency.
- `walletImpact.totalCost: 1198.00` — 1198.00 **GBP**, what the position cost in the
  account currency.
- `walletImpact.unrealizedProfitLoss: 204.50` — 204.50 **GBP**.
- `walletImpact.fxImpact: 12.75` — 12.75 **GBP**, the portion of the unrealized P/L
  attributable to USD/GBP movement rather than to the share price itself.

An entity built from `currentPrice` must carry `unit_of_measurement: USD`; an entity
built from `walletImpact.currentValue` must carry `unit_of_measurement: GBP`. Swapping
them would show a GBP figure labelled USD (or vice versa) — silently wrong, not merely
confusing (`REQ-DM-05`, `REQ-HA-05`).

`walletImpact.fxImpact` is only meaningful when the instrument currency differs from
the account currency; it is published as a diagnostic entity regardless, reading zero
(or near-zero) for same-currency holdings.

## Error responses

| Status | Meaning | Handling |
|---|---|---|
| `401` | Unauthorized — bad or missing credentials | Fatal at startup; mid-run, logged at error and counted toward the readiness threshold |
| `403` | Forbidden — credentials lack access to this account | Same as `401` |
| `408` | Upstream timeout | Transient — retried per the scheduler's retry policy |
| `429` | Rate limited | Back off until `x-ratelimit-reset`, retry once, then treat as a transient failure if still failing |
| `5xx` / network errors | Upstream or transport failure | Transient — retried per the scheduler's retry policy |

`401`/`403` surface as `ErrUnauthorized`; `429` surfaces as `*RateLimitError` carrying
`ResetAt` parsed from `x-ratelimit-reset` (`REQ-T2-06`).

## Deprecated surface

The Pies API (`/equity/pies*`) is formally deprecated upstream and is not consumed by
this project.

## Diagnostic access

A hidden `dump` subcommand issues one authenticated request through the normal client
and auth path and prints the raw response body to **stdout** — never to a file — for
inspecting real API payloads when a field's meaning is unclear (`REQ-T2-09`).
