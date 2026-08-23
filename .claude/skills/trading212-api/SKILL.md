---
name: trading212-api
description: Use when writing or reviewing code that calls the Trading 212 Public API — base URLs, Basic/legacy auth, the two read endpoints and their schemas, x-ratelimit-* handling, and the two-currency rule.
---

# Trading 212 Public API

Reference: <https://docs.trading212.com/api>. The API is in **beta** and is enabled
only for **Invest and Stocks ISA** account types.

## Environments

| Mode | Base URL |
|---|---|
| Live | `https://live.trading212.com/api/v0` |
| Demo (paper trading) | `https://demo.trading212.com/api/v0` |

## Authentication

HTTP Basic, key as username and secret as password:

    Authorization: Basic base64(API_KEY + ":" + API_SECRET)

A legacy scheme sending the bare API key as the whole header value is also
accepted upstream. This project falls back to it when no secret is configured.

Never log either value.

## Rate limiting

Limits are enforced **per account** — not per key, not per IP. Another tool using
the same account shares the budget.

Every response carries:

| Header | Meaning |
|---|---|
| `x-ratelimit-limit` | Requests allowed in the current period |
| `x-ratelimit-period` | Period length in seconds |
| `x-ratelimit-remaining` | Requests left in the period |
| `x-ratelimit-reset` | Unix timestamp when the limit fully resets |
| `x-ratelimit-used` | Requests already made in the period |

The limiter permits bursts within a period rather than strict spacing. On 429,
wait until `x-ratelimit-reset` rather than guessing a backoff.

## Endpoints this project calls

Only these two. This service is strictly read-only; order, cancellation and pie
endpoints must never appear in the tree.

### GET /api/v0/equity/account/summary — 1 req / 5s

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

`id` is the account number shown in the Trading 212 apps. `currency` is ISO 4217
and is the account's primary currency.

### GET /api/v0/equity/positions — 1 req / 1s

Returns an array. An optional `ticker` query parameter filters server-side; this
project does not use it, preferring one unfiltered call plus client-side
filtering.

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

## The two-currency rule

**`averagePricePaid` and `currentPrice` are in `instrument.currency`. Everything
under `walletImpact` is in the account's primary currency.**

A USD holding in a GBP account reports its prices in USD and its value, cost,
P&L and FX impact in GBP. Entities built from these fields must carry the
matching `unit_of_measurement`. Conflating them misreports the position
silently, which is worse than failing loudly.

`walletImpact.fxImpact` is only meaningful when the instrument currency differs
from the account currency; it is published as a diagnostic entity.

## Error responses

`401` / `403` — bad or unauthorised credentials. Fatal at startup.
`408` — upstream timeout. Transient.
`429` — rate limited. Back off until `x-ratelimit-reset`.

## Deprecated surface

The Pies API (`/equity/pies*`) is formally deprecated upstream and is not
consumed by this project.
