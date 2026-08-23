# trading212-mqtt — Design

**Date:** 2026-08-23
**Status:** Approved, ready for implementation planning

## Purpose

A small, stdlib-first Go service that polls the **Trading 212 Public API** and
republishes account and position metrics to **MQTT** with **Home Assistant (HA) MQTT
autodiscovery**, so a Trading 212 Invest / Stocks ISA account appears in Home Assistant
as a device with cash, value and profit-and-loss entities — plus one nested device per
whitelisted holding — with no manual HA configuration.

Deployed to Kubernetes as a **stateless** Deployment. Manifests live outside this repo.

## Principles

- **Spec-driven.** `docs/specs/*` are authored first and are the source of truth. Code is
  reconciled against `REQUIREMENTS.md` via `/spec-reconcile`.
- **stdlib-first.** Latest Go. The only external dependencies are cobra (CLI), autopaho
  (MQTT), godotenv (`.env`) and godog (BDD, test-only). HTTP, JSON, crypto, scheduling and
  logging all come from the standard library.
- **Strictly read-only.** The service calls only `GET /equity/account/summary` and
  `GET /equity/positions`. No order placement, cancellation or pie mutation code exists in
  the tree. This is asserted by a requirement, not just by convention.
- **No on-disk state.** Nothing is written to disk at runtime: no token cache, no
  spooled fixtures, no writable volume. The container runs non-root with a read-only root
  filesystem. Retained MQTT messages are the only persistence, held by the broker across
  pod restarts. `.env` is a local-development convenience; in-cluster configuration comes
  from environment variables and Secrets.
- **Kube-friendly.** HTTP liveness and readiness probes, environment-based config,
  structured logging (`log/slog`), graceful shutdown, distroless image.

## Non-goals

- Any form of trading, order management or pie mutation.
- Pies as a data source. The Pies API is formally deprecated upstream and is not consumed.
- Historical endpoints (orders, dividends, transactions, CSV exports).
- Multiple accounts per process. One process serves one Trading 212 account.
- Kubernetes manifests, Helm charts, or MQTT broker provisioning.

## The Trading 212 API

Documented at <https://docs.trading212.com/api>. The API is in **beta** and is enabled
only for **Invest and Stocks ISA** account types.

**Environments.** Live: `https://live.trading212.com/api/v0`. Paper trading (demo):
`https://demo.trading212.com/api/v0`.

**Authentication.** HTTP Basic: `Authorization: Basic base64(API_KEY:API_SECRET)`. A
legacy scheme sending the bare API key as the `Authorization` value is also accepted
upstream and is supported as a fallback when no secret is configured.

**Rate limiting.** Enforced **per account**, not per key or per IP — a second consumer of
the same account shares the budget. Every response carries `x-ratelimit-limit`,
`x-ratelimit-period`, `x-ratelimit-remaining`, `x-ratelimit-reset` (Unix timestamp) and
`x-ratelimit-used`. The limiter allows bursts within a period rather than strict spacing.

**Endpoints consumed** (the only two):

| Endpoint | Limit | Purpose |
|---|---|---|
| `GET /api/v0/equity/account/summary` | 1 req / 5s | Account totals, id, primary currency |
| `GET /api/v0/equity/positions` | 1 req / 1s | All open positions |

`GET /equity/positions` accepts an optional `ticker` query parameter. This design does not
use it: one unfiltered call returning every position is cheaper than one call per
whitelisted ticker, and filtering happens client-side.

### Response shapes

`GET /equity/account/summary`:

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

`GET /equity/positions` returns an array of:

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

### The two-currency rule

`averagePricePaid` and `currentPrice` are denominated in the **instrument's** currency
(`instrument.currency`). Every field under `walletImpact` is denominated in the **account's**
primary currency (`walletImpact.currency`, which matches `summary.currency`).

Price entities and value entities therefore carry different `unit_of_measurement` values,
resolved per position when discovery is published. Conflating them would silently misreport
a USD holding inside a GBP account, so this is called out as its own requirement.

## Repository layout

```
cmd/main.go                     thin entrypoint; delegates to internal/cmd
internal/cmd/                   root (signal context, .env), serve, mock, dump
internal/config/                env -> Config, MODE switch, secret-redacting String()
internal/trading212/            client.go, model.go, parse.go, constants.go, ratelimit.go
internal/scheduler/             poll loop
internal/homeassistant/         discovery.go, entities.go (the entity catalogue)
internal/mqtt/                  autopaho backend, LWT wiring
internal/publisher/             Publisher interface + recording fake
internal/server/                / status page, /healthz, /readyz
internal/mock/                  standalone fake Trading 212 API, shared with the e2e suite
features/                       godog acceptance suite
docs/specs/                     numbered specs + REQUIREMENTS.md (source of truth)
.claude/                        skills, commands, agents
```

Each package has one clear job and a narrow interface. `publisher.Publisher` is the seam
that makes the whole pipeline testable without a broker; `trading212.Client` is the seam
that makes it testable without credentials.

## Configuration

All configuration is environment-based. `godotenv` loads a local `.env` for development;
`.env.dist` ships a working `MODE=mock` default so the pipeline runs with no credentials.

| Variable | Default | Meaning |
|---|---|---|
| `MODE` | `live` | `live`, `demo` or `mock` — resolves the API base URL |
| `MOCK_URL` | `http://localhost:8090` | Base URL when `MODE=mock` |
| `T212_API_KEY` | — | Required in `live`/`demo` |
| `T212_API_SECRET` | — | Basic-auth password; empty selects the legacy header scheme |
| `TICKERS` | empty | Position whitelist (see below) |
| `POLL_INTERVAL` | `5m` | Interval between polls; first poll runs immediately |
| `MQTT_BROKER_URL` | — | Required in every mode |
| `MQTT_USERNAME` / `MQTT_PASSWORD` | empty | Broker credentials |
| `MQTT_CLIENT_ID` | `trading212-mqtt` | MQTT client identifier |
| `TOPIC_PREFIX` | `trading212` | Root of all state topics |
| `DISCOVERY_PREFIX` | `homeassistant` | HA discovery prefix |
| `HTTP_ADDR` | `:8080` | Status page and probe listener |
| `READY_FAILURE_THRESHOLD` | `3` | Consecutive poll failures before `/readyz` goes unready |
| `LOG_LEVEL` | `info` | `log/slog` level |

`MODE` is a single switch rather than raw URL overrides, so there is exactly one way to
point the service at a non-live environment.

**`TICKERS` semantics.** Empty publishes account entities only and creates no position
devices — the safe default, since an account with hundreds of holdings would otherwise
carpet-bomb Home Assistant on first run. A comma-separated list
(`AAPL_US_EQ,VUSA_EQ`) creates one device per listed ticker, at startup, whether or not the
position is currently held. The literal `*` instead tracks whatever is held: devices are
discovered from poll results, so a position opened while the service is running gets its
discovery configs published at the poll that first sees it, and a position that disappears
keeps its device but flips to `offline` availability. With an explicit list, the set of
devices is fixed for the process lifetime; with `*`, it grows as holdings appear.

`Config.String()` redacts `T212_API_KEY`, `T212_API_SECRET` and `MQTT_PASSWORD`. Neither
credentials nor the full account id are ever logged; the status page masks the account id
to its last four digits.

## MQTT topics

```
trading212/<account_id>/state                              retained JSON, account totals
trading212/<account_id>/availability                        retained online|offline (LWT)
trading212/<account_id>/positions/<ticker>/state            retained JSON, one position
trading212/<account_id>/positions/<ticker>/availability     retained online|offline
```

One state topic per device, rather than a single account-wide document with positions
nested inside it. Each discovery config points at its own topic via a `~` base-topic
abbreviation, keeping `value_template` expressions one level deep, and — more importantly —
giving every position device an availability topic of its own.

All discovery, state and availability publishes are **retained** at **QoS 1**.

## Home Assistant discovery

Per-entity discovery topics: `<discovery_prefix>/<component>/<node_id>/<object_id>/config`,
retained, QoS 1, published once at startup and again after any MQTT reconnect.

### Devices

**Account device.** `identifiers: ["t212_<account_id>"]`, name `Trading 212 (<account_id>)`,
manufacturer `Trading 212`.

**Position devices.** One per whitelisted ticker:
`identifiers: ["t212_<account_id>_<ticker>"]`, name `Trading 212 – <instrument.name>`,
`via_device: t212_<account_id>`. Home Assistant then nests each holding under the account
device.

### Account entities

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

Monetary entities use the account primary currency as `unit_of_measurement`.

`return_pct` is derived as `unrealizedProfitLoss / totalCost × 100`, and is `nil` when
`totalCost` is zero.

### Position entities

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

Position `return_pct` is `walletImpact.unrealizedProfitLoss / walletImpact.totalCost × 100`,
`nil` when `totalCost` is zero.

### Availability

Every entity lists **two** availability topics with `availability_mode: all`:

1. The service-level topic, `trading212/<account_id>/availability`, registered as the MQTT
   Last Will and Testament with a retained `offline` payload and published `online` on
   connect.
2. For position entities only, the position-level topic
   `trading212/<account_id>/positions/<ticker>/availability`.

A whitelisted ticker that is not currently held publishes retained `offline` on its own
availability topic. Home Assistant greys out exactly that device and leaves the rest of the
account untouched. Selling out of a holding and buying back into it later needs no
discovery churn — no config is deleted or re-added, only the availability payload flips.

## Runtime flow

**Startup.** Load and validate config → construct `trading212.Client` → issue one
`GET /equity/account/summary` to validate credentials and learn the account id and primary
currency (both are prerequisites for discovery: the id is the device identifier, the
currency is every monetary entity's unit) → publish discovery for the account device and
each whitelisted position device → start the scheduler → start the HTTP server.

**Each poll** is exactly two API calls: `GET /equity/account/summary` and
`GET /equity/positions`. Positions are filtered client-side against `TICKERS`. Results are
published as one retained message on the account state topic plus one per whitelisted
ticker, and availability is flipped for any whitelisted ticker whose held/not-held status
changed. The first poll runs immediately at startup rather than after one interval.

**Rate limiting.** `internal/trading212/ratelimit.go` records the `x-ratelimit-*` headers
from every response and enforces a per-endpoint minimum spacing (5s for summary, 1s for
positions). On a 429 it backs off until `x-ratelimit-reset` and retries once. At the default
5-minute interval this never engages; it exists so that a shorter interval, a restart loop,
or a manual `dump` cannot throttle the account — and limits are per account, so throttling
would also affect anything else the user runs against the same account.

**Shutdown** on `SIGTERM`/`SIGINT`: publish retained `offline` to every availability topic,
disconnect MQTT cleanly, exit 0.

**Subcommands.** `serve` runs the service. `mock` runs the fake API standalone, so the full
pipeline can be exercised locally without credentials. `dump` is a hidden diagnostic that
issues a single authenticated request and writes the raw JSON response to **stdout** — never
to a file, keeping the no-on-disk-state rule intact — for inspecting real API payloads when a
field's meaning is unclear.

## Error handling

- **Transient HTTP or decode failures.** Log at warn, skip the publish. Retained state keeps
  its last-known values rather than being overwritten with nulls.
- **Readiness.** After `READY_FAILURE_THRESHOLD` consecutive poll failures, `/readyz` reports
  not-ready. A single success clears the counter.
- **401 / 403.** Fatal during startup validation — bad credentials should crash-loop visibly
  rather than run blind. Mid-run, they log at error and count toward the readiness threshold.
- **429.** Back off until `x-ratelimit-reset`, retry once, then treat as a transient failure.
- **MQTT disconnect.** autopaho reconnects automatically. On reconnect, discovery configs and
  `online` availability are republished — cheap, and it heals a broker that lost its retained
  set.
- **Whitelisted ticker never seen.** Not an error. Its device is created at startup and its
  availability stays `offline` until the position appears.

## Lifecycle and health

`internal/server` exposes `/healthz` (liveness — process is up), `/readyz` (readiness — MQTT
connected and the poll-failure counter under threshold) and `/` (an HTML status page showing
mode, masked account id, last poll time, last error and current entity counts). The root
context comes from `signal.NotifyContext`; shutdown waits for the scheduler to finish any
in-flight poll before disconnecting.

## Testing

**`internal/mock`** implements `/api/v0/equity/account/summary` and `/api/v0/equity/positions`
from canned fixtures in `internal/trading212/testdata/`. The fixture set includes a
cross-currency holding (USD instrument in a GBP account) so the two-currency rule is exercised
by default, a zero-cost position so `return_pct` division-by-zero is covered, and a whitelisted
ticker that is absent from the positions array. The mock backs both the `mock` subcommand and
the acceptance suite.

**Unit tests** cover parsing, config resolution (including `MODE` and `TICKERS` semantics), the
rate limiter, derived-field maths, and golden discovery payloads.

**`features/`** drives the real `serve` wiring against the mock with the recording `Publisher` —
no broker required — asserting topics, payloads, availability transitions and readiness
behaviour.

`make test` runs unit and integration tests, excluding `features/`, so the common loop stays
fast. `make test-e2e` runs the godog suite. `make lint` runs golangci-lint from a pinned binary
in `./bin`.

## Deployment

Multi-stage distroless image, non-root, read-only root filesystem, no volumes. `MODE` defaults
to `live` in the image. Configuration and credentials arrive as environment variables and
Secrets. Liveness probes `/healthz`, readiness probes `/readyz`. Manifests are maintained in a
separate repository. Releases are cut by release-please.

Because the deployment is stateless and holds nothing on disk, a restarted or rescheduled pod
recovers by republishing discovery and polling immediately; retained broker state covers the gap
in between.

## Spec-driven workflow

`docs/specs/*.md` are authored before code and are the source of truth.
`docs/specs/REQUIREMENTS.md` holds stable `REQ-*` IDs, each with a one-line requirement and a
`→ file` implementation pointer. Behaviour changes update the spec and the `REQ-*` entry in the
same change. `/spec-reconcile` maps IDs to code and reports drift.

Spec documents:

| File | Contents |
|---|---|
| `00-overview.md` | Purpose, principles, non-goals, high-level flow |
| `01-trading212-api.md` | Environments, auth, the two endpoints, rate-limit headers, the two-currency rule |
| `02-domain-model.md` | `AccountSummary`, `Position`, derived fields, optional-field conventions |
| `03-mqtt-ha-discovery.md` | Topics, device blocks, `via_device`, entity catalogue, availability |
| `04-polling-scheduling.md` | Poll loop, immediate first run, rate limiting, filtering |
| `05-config.md` | Environment variables, `MODE`, `TICKERS`, redaction |
| `06-lifecycle-health.md` | Probes, readiness rules, graceful shutdown, status page |
| `07-testing.md` | Mock server, fixtures, unit tests, godog suite |
| `08-deployment.md` | Image, statelessness, probes, release process |
| `REQUIREMENTS.md` | `REQ-*` IDs |

Requirement prefixes: `T2` API client, `DM` domain model, `HA` MQTT/Home Assistant, `SC`
scheduling, `CF` config, `LC` lifecycle/health, `TS` testing, `DP` deployment, `AS` Claude
assets.

## Claude assets

Authored **before** any Go code, so the spec documents and the implementation are written under
the conventions from the start.

| Asset | Purpose |
|---|---|
| `.claude/skills/effective-go/` | Idiomatic Go conventions for this repo: package boundaries, accept-interfaces/return-structs, error wrapping, context propagation, table tests |
| `.claude/skills/go-1.26/` | Current-toolchain idioms, validated against the installed Go version |
| `.claude/skills/home-assistant-mqtt-discovery/` | Discovery topics, device blocks, `via_device`, availability and `availability_mode`, device/state classes — source of truth for `internal/homeassistant` |
| `.claude/skills/trading212-api/` | The API contract: environments, Basic and legacy auth, the two read endpoints and their schemas, `x-ratelimit-*` handling, the two-currency rule, beta caveats |
| `.claude/commands/spec-reconcile.md` | Read-only drift report mapping `REQ-*` to code |
| `.claude/agents/go-http-pattern-planner.md` | Read-only planner reviewing HTTP-server surfaces against Mat Ryer's Go HTTP-services pattern |
| `CLAUDE.md` | Repository guidance: what this is, the spec-driven workflow, commands, architecture, conventions |

Every asset is written with Trading 212 examples. **No file in this repository references any
other project by name.** The design borrows its shape from a prior poll-to-MQTT service, but that
provenance belongs to the commit history, not to a standing cross-reference the project has to
keep explaining.

## Build order

1. `.claude` assets and `CLAUDE.md`.
2. `docs/specs/*` including `REQUIREMENTS.md`.
3. Go module skeleton, `internal/config`, Makefile, lint configuration.
4. `internal/trading212`: model, client, parsing, rate limiter — with fixtures and unit tests.
5. `internal/mock` and the `mock` subcommand.
6. `internal/publisher`, `internal/mqtt`, `internal/homeassistant` — discovery and the entity
   catalogue.
7. `internal/scheduler` and `internal/cmd/serve.go` wiring.
8. `internal/server`: probes and status page.
9. `features/` acceptance suite.
10. Dockerfile, CI, release-please.
