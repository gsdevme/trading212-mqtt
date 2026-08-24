# REQUIREMENTS

This is the traceability index for `trading212-mqtt`. Every requirement below has a
stable **`REQ-<prefix>-<nn>`** ID, a one-sentence statement of what it requires
(naming the concrete token to grep for when checking compliance), and a `→ file`
pointer to where it is implemented. Code is reconciled against this list by
`/spec-reconcile`, which maps each ID to code and reports drift — an ID's wording and
pointer change only in the same commit as the behaviour they describe, never on their
own.

Prefix meanings:

| Prefix | Area | Spec |
|---|---|---|
| `T2` | API client | `01-trading212-api.md` |
| `DM` | Domain model | `02-domain-model.md` |
| `HA` | MQTT / Home Assistant | `03-mqtt-ha-discovery.md` |
| `SC` | Scheduling | `04-polling-scheduling.md` |
| `CF` | Config | `05-config.md` |
| `LC` | Lifecycle & health | `06-lifecycle-health.md` |
| `TS` | Testing | `07-testing.md` |
| `DP` | Deployment | `08-deployment.md` |
| `AS` | Claude assets | design doc, "Claude assets" |

## API client (`internal/trading212`)

- **REQ-T2-01** Base URLs are `https://live.trading212.com/api/v0` and
  `https://demo.trading212.com/api/v0`, defined as constants. → `constants.go`
- **REQ-T2-02** `Authorization: Basic base64(key:secret)`; when the secret is empty
  the bare key is sent as the whole header value (legacy scheme). → `client.go`
- **REQ-T2-03** `AccountSummary(ctx)` issues `GET /equity/account/summary`. → `client.go`
- **REQ-T2-04** `Positions(ctx)` issues `GET /equity/positions` with no `ticker`
  parameter. → `client.go`
- **REQ-T2-05** No write endpoint (`POST`, `DELETE`, `/orders`, `/pies`) exists
  anywhere in the tree. → whole tree
- **REQ-T2-06** 401/403 surface `ErrUnauthorized`; 429 surfaces `*RateLimitError`
  carrying `ResetAt` from `x-ratelimit-reset`. → `client.go`
- **REQ-T2-07** Per-endpoint limiter enforces 5s spacing for summary and 1s for
  positions, and waits until `ResetAt` after a 429. → `ratelimit.go`
- **REQ-T2-08** Every response's `x-ratelimit-*` headers are observed by the
  limiter. → `ratelimit.go`, `client.go`
- **REQ-T2-09** Hidden `dump` diagnostic prints a raw response to stdout via the
  normal auth path and never writes a file. → `cmd/dump.go`, `client.go`

## Domain model (`internal/trading212`)

- **REQ-DM-01** `AccountSummary` carries id, currency, total value, the three cash
  figures, the four investment figures, derived `ReturnPct` and `LastUpdated`. → `model.go`
- **REQ-DM-02** `Position` carries instrument identity, both currencies, quantities,
  both prices, the four wallet-impact figures, derived `ReturnPct` and `Opened`. → `model.go`
- **REQ-DM-03** Optional/derived fields are pointers so absent differs from zero. → `model.go`
- **REQ-DM-04** `ReturnPct` is `unrealized / cost * 100`, and is `nil` when cost is
  zero. → `model.go`
- **REQ-DM-05** Price fields are denominated in `InstrumentCurrency`; wallet-impact
  fields in `AccountCurrency`. → `parse.go`, `model.go`
- **REQ-DM-06** `Slug` lower-cases a ticker and replaces every character outside
  `[a-z0-9_]` with `_`. → `model.go`
- **REQ-DM-07** `Whitelist`: empty tracks nothing, `*` tracks all held positions, a
  comma-separated list tracks exactly those tickers (case-insensitive). → `model.go`
- **REQ-DM-08** Wire DTOs are unexported and map into the domain types; the domain
  types' JSON tags are the published state document's field names. → `parse.go`

## MQTT & Home Assistant (`internal/mqtt`, `internal/homeassistant`, `internal/publisher`)

- **REQ-HA-01** Per-entity discovery topics
  `<discovery_prefix>/<component>/<node_id>/<object_id>/config`, retained, QoS 1. → `homeassistant/discovery.go`
- **REQ-HA-02** One retained JSON state topic per device; entities read it via
  `value_template`. → `publisher/publisher.go`, `homeassistant/discovery.go`
- **REQ-HA-03** Account device `identifiers: ["t212_<account_id>"]`; position devices
  `identifiers: ["t212_<account_id>_<slug>"]` with `via_device` naming the account
  device. → `homeassistant/discovery.go`
- **REQ-HA-04** Account entity catalogue per `03-mqtt-ha-discovery.md`, with the
  account currency as `unit_of_measurement` on monetary entities. → `homeassistant/entities.go`
- **REQ-HA-05** Position entity catalogue per `03-mqtt-ha-discovery.md`; `avg_price`
  and `current_price` use the instrument currency, all wallet-impact entities the
  account currency. → `homeassistant/entities.go`
- **REQ-HA-06** Service availability via LWT (retained `offline`), `online` on
  connect, retained `offline` on graceful shutdown. → `mqtt/client.go`, `cmd/serve.go`
- **REQ-HA-07** Position entities list both the service and position availability
  topics with `availability_mode: all`. → `homeassistant/discovery.go`
- **REQ-HA-08** A tracked ticker that is not currently held publishes retained
  `offline` to its own availability topic and keeps its discovery configs. → `publisher/publisher.go`
- **REQ-HA-09** A statically whitelisted ticker gets placeholder discovery at
  startup (name = ticker, instrument currency = account currency), republished with
  real instrument metadata on first sighting. → `publisher/publisher.go`
- **REQ-HA-10** `entity_category: diagnostic` on realized P/L, both non-tradeable
  cash figures, last-updated, cost, FX impact, quantity-in-pies and opened. → `homeassistant/entities.go`
- **REQ-HA-11** All discovery, state and availability publishes are retained at
  QoS 1. → `mqtt/client.go`, `publisher/publisher.go`
- **REQ-HA-12** `Publisher` is an interface with an autopaho backend and a recording
  fake. → `publisher/publisher.go`, `publisher/recording.go`
- **REQ-HA-13** Every MQTT (re)connection republishes the service `online`, the
  account discovery, and — for each tracked position — its discovery rebuilt from the
  stored instrument metadata plus its last-known availability, so a refined, held
  position comes back refined and `online`. Statically whitelisted tickers never yet
  seen still get placeholder discovery and `offline`. This is a distinct entry point
  from the startup routine (`Service.Republish`, not `PublishDiscovery`). →
  `publisher/publisher.go`, `mqtt/client.go`, `cmd/serve.go`

## Scheduling (`internal/scheduler`)

- **REQ-SC-01** Poll loop runs immediately then every `POLL_INTERVAL`. → `scheduler.go`
- **REQ-SC-02** Each poll makes exactly two API calls and produces one `Snapshot`. → `scheduler.go`
- **REQ-SC-03** Transient fetch failures retry up to `POLL_MAX_RETRIES` with
  exponential backoff from 1s. → `scheduler.go`
- **REQ-SC-04** A failed poll skips the publish, leaving retained state at its last
  known values. → `scheduler.go`
- **REQ-SC-05** Poll outcomes are reported to the health reporter. → `scheduler.go`
- **REQ-SC-06** Positions are filtered against the whitelist client-side. → `publisher/publisher.go`

## Config (`internal/config`)

- **REQ-CF-01** `MODE` resolves to the live, demo or mock base URL; anything else is
  a validation error. → `config.go`
- **REQ-CF-02** `T212_API_KEY` is required in live and demo; mock supplies dummies. → `config.go`
- **REQ-CF-03** `MQTT_BROKER_URL` is required in every mode and must parse. → `config.go`
- **REQ-CF-04** `POLL_INTERVAL` defaults to `5m` with a `1m` floor. → `config.go`
- **REQ-CF-05** `TICKERS` parses into a `Whitelist`. → `config.go`
- **REQ-CF-06** `READY_FAILURE_THRESHOLD` defaults to 3 and must be at least 1. → `config.go`
- **REQ-CF-07** Validation collects every error via `errors.Join`. → `config.go`
- **REQ-CF-08** `String()` redacts both credentials and the MQTT password. → `config.go`

## Lifecycle & health (`internal/server`, `internal/cmd`)

- **REQ-LC-01** `/healthz` returns 200 while the process runs. → `server/server.go`
- **REQ-LC-02** `/readyz` is ready after the first successful publish and not-ready
  after `READY_FAILURE_THRESHOLD` consecutive failures. → `server/server.go`
- **REQ-LC-03** The HTTP listener starts before the first API call so probes answer
  during init. → `cmd/serve.go`
- **REQ-LC-04** A failed startup validation call is fatal: the process logs and exits
  non-zero rather than starting a scheduler with nothing to publish. The one exception
  is the root context already being cancelled — a `SIGTERM` landing mid-startup, which
  is a normal termination and exits 0. → `cmd/serve.go`
- **REQ-LC-05** `SIGTERM`/`SIGINT` publishes retained `offline` to every availability
  topic — every position attempted, and the service topic written unconditionally even
  if a position publish fails — then disconnects cleanly and exits 0. →
  `cmd/root.go`, `cmd/serve.go`, `publisher/publisher.go`
- **REQ-LC-06** The status page masks the account id to its last 4 digits. → `server/page.go`
- **REQ-LC-07** Nothing is written to disk at runtime. → whole tree

## Testing (`internal/mock`, `features`)

- **REQ-TS-01** The mock serves both endpoints with canned payloads built from
  `Options`. → `mock/server.go`, `mock/routes.go`
- **REQ-TS-02** Fixtures include a cross-currency holding, a zero-cost position and
  a whitelisted-but-absent ticker. → `mock/server.go`, `trading212/testdata/`
- **REQ-TS-03** The mock can be made to fail so degradation is testable. → `mock/server.go`
- **REQ-TS-04** The godog suite drives the real wiring against the mock and the
  recording publisher. → `features/steps_test.go`
- **REQ-TS-05** `make test` excludes `features/`; `make test-e2e` runs it. → `Makefile`

## Deployment (`Dockerfile`, CI)

- **REQ-DP-01** Multi-stage distroless image, non-root, static binary. → `Dockerfile`
- **REQ-DP-02** Image defaults `MODE=live` and exposes 8080. → `Dockerfile`
- **REQ-DP-03** No manifests, charts or broker provisioning in this repo. → whole tree
- **REQ-DP-04** CI runs lint, unit tests and the acceptance suite on every PR. → `.github/workflows/`
- **REQ-DP-05** Releases are cut by release-please; images push on release only. → `.github/workflows/release.yml`

## Claude assets (`.claude`)

- **REQ-AS-01** Skills exist for Go conventions, the current toolchain, HA
  discovery, and the Trading 212 API. → `.claude/skills/`
- **REQ-AS-02** `/spec-reconcile` maps `REQ-*` to code, read-only. → `.claude/commands/spec-reconcile.md`
- **REQ-AS-03** A read-only HTTP-pattern planner agent exists. → `.claude/agents/go-http-pattern-planner.md`
- **REQ-AS-04** No file in the repository references another project by name. → whole tree
