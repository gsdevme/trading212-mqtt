# trading212-mqtt

Polls the Trading 212 Public API and republishes account and position metrics to
MQTT with Home Assistant autodiscovery. It is **strictly read-only**: the service
calls exactly two `GET` endpoints (`/equity/account/summary` and
`/equity/positions`) and no code path anywhere in the tree can place, modify or
cancel an order. It is also **stateless** — nothing is written to disk at
runtime; the only thing that outlives a single process run is whatever the MQTT
broker retains.

> **Read-only, by construction.** Point this at a real brokerage account with
> confidence: there is no write endpoint implemented, no order path, no pie
> management, and no on-disk state to leak or corrupt. See
> [`docs/specs/01-trading212-api.md`](docs/specs/01-trading212-api.md) for the
> exact two endpoints this service is allowed to call.

## What you get in Home Assistant

One **account device** (`Trading 212 (<account_id>)`), carrying:

| Entity | Meaning |
|---|---|
| `total_value` | Total account value |
| `free_cash` | Cash available to trade |
| `invested` | Total cost basis of all holdings |
| `current_value` | Current market value of all holdings |
| `unrealized_pl` | Unrealized profit/loss across all holdings |
| `return_pct` | Derived return percentage (unrounded; shown to 2dp in Home Assistant) |
| `realized_pl` *(diagnostic)* | Realized profit/loss |
| `cash_in_pies` *(diagnostic)* | Cash allocated to Pies |
| `cash_reserved` *(diagnostic)* | Cash reserved by open orders |
| `last_updated` *(diagnostic)* | Time of the last successful poll |

...and one **position device per tracked ticker**, nested under the account
device via Home Assistant's `via_device` linkage (`Trading 212 – <instrument
name>`), carrying:

| Entity | Meaning |
|---|---|
| `quantity` | Shares held |
| `avg_price` | Average price paid per share |
| `current_price` | Current market price per share |
| `value` | Current value of the position |
| `unrealized_pl` | Unrealized profit/loss on the position |
| `return_pct` | Derived return percentage (unrounded; shown to 2dp in Home Assistant) |
| `cost` *(diagnostic)* | Cost basis of the position |
| `fx_impact` *(diagnostic)* | Portion of the P/L attributable to FX movement |
| `quantity_in_pies` *(diagnostic)* | Shares held inside a Pie |
| `opened` *(diagnostic)* | When the position was first opened |

**`avg_price` and `current_price` carry the instrument's own currency as their
`unit_of_measurement`; every other monetary entity — `value`, `cost`,
`unrealized_pl`, `fx_impact` — carries the account's primary currency.** These
are frequently different currencies for the same position, and nothing in the
underlying API response prevents conflating them, so every entity is built and
labelled from the field it actually came from. The normative statement of this
rule lives in [`docs/specs/01-trading212-api.md`](docs/specs/01-trading212-api.md).

## Requirements

- A Trading 212 **Invest** or **Stocks ISA** account (the Public API is not
  enabled for other account types).
- An **API key** (and secret, unless using the legacy bare-key scheme) generated
  from your Trading 212 account settings.
- An **MQTT broker** that your Home Assistant instance is already connected to.

## Quick start (no credentials)

You can run the full pipeline with no Trading 212 credentials at all, against
the bundled mock API:

```sh
cp .env.dist .env   # ships MODE=mock, TICKERS empty
make run-mock       # terminal 1: fake Trading 212 API on :8090
make run            # terminal 2: the service (needs an MQTT broker at :1883)
```

With `TICKERS` empty, expect retained discovery configs and a state topic for
the account device only, plus `online` availability, on the broker:

```sh
mosquitto_sub -t 'homeassistant/#' -t 'trading212/#' -v -W 3
```

Set `TICKERS=AAPL_US_EQ,VUSA_EQ` (or any tickers the mock's canned data holds)
before `make run` to also see position devices appear. The `held=` attribute on
each `published snapshot` log line, and the holdings table on
<http://localhost:8080>, list exactly what the account holds — see
[Finding your tickers](#finding-your-tickers).

## Configuration

All configuration is environment-based. `godotenv` loads a local `.env` for
development; `.env.dist` documents every variable with its default.

| Variable | Default | Meaning |
|---|---|---|
| `MODE` | `live` | `live`, `demo` or `mock` — resolves the API base URL |
| `MOCK_URL` | `http://localhost:8090` | Base URL when `MODE=mock` |
| `T212_API_KEY` | — | Required in `live`/`demo` |
| `T212_API_SECRET` | — | Basic-auth password; empty selects the legacy header scheme |
| `TICKERS` | empty | Position whitelist (see below) |
| `POLL_INTERVAL` | `5m` | Interval between polls; values below `1m` are rejected; first poll runs immediately |
| `POLL_MAX_RETRIES` | `3` | Retries per poll on transient failure; must be a valid integer `>= 0` |
| `MQTT_BROKER_URL` | — | Required in every mode |
| `MQTT_USERNAME` / `MQTT_PASSWORD` | empty | Broker credentials |
| `MQTT_CLIENT_ID` | `trading212-mqtt` | MQTT client identifier |
| `TOPIC_PREFIX` | `trading212` | Root of all state topics |
| `DISCOVERY_PREFIX` | `homeassistant` | HA discovery prefix |
| `HTTP_ADDR` | `:8080` | Status page and probe listener |
| `READY_FAILURE_THRESHOLD` | `3` | Consecutive poll failures before `/readyz` goes unready; a valid integer, minimum `1` |
| `LOG_LEVEL` | `info` | `log/slog` level |
| `LOG_FORMAT` | `json` | `log/slog` output format — `json` or `text` |

### `TICKERS` — position whitelist

This is the one setting whose behaviour is not obvious, so it is spelled out in
full:

- **Empty** (the default) publishes account entities only and creates **no**
  position devices. This is the safe default — an account with hundreds of
  holdings would otherwise carpet-bomb Home Assistant with devices on first
  run.
- **The literal `*`** tracks **every currently-held position**. Devices are
  discovered lazily from poll results: a position opened while the service is
  running gets its discovery configs published at the poll that first sees it,
  and a position that disappears keeps its device but flips to `offline`
  availability.
- **A comma-separated list** (e.g. `AAPL_US_EQ,VUSA_EQ`) tracks **exactly**
  those tickers, case-insensitively, and creates one device per listed ticker
  at startup whether or not the position is currently held. A listed ticker
  that isn't held gets a placeholder device (named after the raw ticker,
  using the account's currency as a stand-in) that reports `offline`
  availability until the position is first seen, at which point its discovery
  is republished with the real instrument name and currency.

With an explicit list, the set of devices is fixed for the process lifetime;
with `*`, it grows as holdings appear.

#### Finding your tickers

`TICKERS` takes Trading 212's own ticker IDs (`AAPL_US_EQ`, `VUSA_EQ`), which are
not the symbols shown in the app. You do not need to look them up — start with
`TICKERS=` empty and let the service tell you:

- Every poll logs them, comma-joined and ready to paste:

  ```
  INFO published snapshot total_value=15234.56 positions=3 held=AAPL_US_EQ,FREE_EQ,VUSA_EQ
  ```

- The status page on `/` lists the same holdings in a table — ticker, name, value,
  and whether each one is currently tracked — followed by the `TICKERS=` line to
  copy.

Paste the list (or the subset you want) into `.env` or your Deployment env and
restart; those positions become Home Assistant devices.

## Running in Kubernetes

The published image is `ghcr.io/gsdevme/trading212-mqtt`, tagged per release
(plus `:latest`). Point a liveness probe at `/healthz` and a readiness probe at
`/readyz` on `HTTP_ADDR` (`:8080` by default); probe timing (interval,
threshold, initial delay) is a manifest concern this repository doesn't
prescribe.

The deployment is **stateless and needs no volume**: nothing is ever written to
disk, so the container can run with a **read-only root filesystem**. It runs as
a non-root user in a distroless image with no shell and no package manager.

No Kubernetes manifests, Helm charts or MQTT broker provisioning live in this
repository — it produces exactly one deployable artifact, the container image.
Whatever runs it (Deployment spec, resource limits, secret wiring, broker
address) is defined and maintained outside this project.

## Rate limits

Trading 212's rate limits are enforced **per account**, not per API key and not
per process — a second consumer of the same account (the Trading 212 app, or
another integration) shares the same budget. The service calls exactly two
endpoints:

| Endpoint | Limit | Purpose |
|---|---|---|
| `GET /api/v0/equity/account/summary` | 1 request / 5s | Account totals, id, primary currency |
| `GET /api/v0/equity/positions` | 1 request / 1s | All open positions |

and rate-limits itself defensively on top of whatever the upstream API allows,
including honouring `x-ratelimit-reset` after a `429`. This is also why
`POLL_INTERVAL` has a one-minute floor: an over-eager interval wouldn't just
risk throttling this service, it would throttle every other tool sharing the
same account's credentials — including the person using the Trading 212 apps.
A misconfigured `POLL_INTERVAL` below the floor is rejected at startup rather
than silently clamped.

## Development

```sh
make build        # -> ./bin/trading212-mqtt
make run          # build + serve (poll -> MQTT; honours MODE from .env)
make run-mock     # build + run the standalone mock Trading 212 API
make test         # unit + integration, with the race detector (excludes godog features)
make test-e2e     # godog acceptance suite (./features/...)
make lint         # golangci-lint (installs the pinned binary into ./bin on first use)
```

[`docs/specs/`](docs/specs/) is the source of truth for this service's
behaviour — code is reconciled against it, not the other way around — and
[`CLAUDE.md`](CLAUDE.md) documents the working conventions for changing it.
