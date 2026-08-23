# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code
in this repository.

## What this is

A stdlib-first Go service (go1.26.2) that polls the **Trading 212 Public API** and
republishes account and position metrics to **MQTT** with **Home Assistant
autodiscovery**. Read-only — it calls exactly two GET endpoints and can never place
or cancel an order. Deployed to Kubernetes as a stateless Deployment; manifests live
in a **separate repo** (none here).

## Spec-driven workflow (read this first)

`docs/specs/*.md` are authored first and are the **source of truth**; code is
reconciled against them. `docs/specs/REQUIREMENTS.md` holds stable `REQ-*` IDs
(prefixes: `T2` API client, `DM` domain model, `HA` MQTT/Home Assistant, `SC`
scheduling, `CF` config, `LC` lifecycle, `TS` testing, `DP` deployment, `AS` Claude
assets), each pointing at its implementation file. When you change behaviour, update
the relevant spec and `REQ-*` entry in the same change. `/spec-reconcile` maps
`REQ-*` to code and flags drift — run it after non-trivial changes.

API behaviour is captured in the `trading212-api` skill, which is the source of
truth for endpoints, auth, rate limits and the two-currency rule.

## Commands

```sh
make build        # -> ./bin/trading212-mqtt
make run          # build + serve (poll -> MQTT; honours MODE from .env)
make run-mock     # build + run the standalone mock Trading 212 API
make test         # unit + integration (excludes the godog features suite)
make test-e2e     # godog acceptance suite (./features/...)
make lint         # golangci-lint (installs pinned binary into ./bin on first use)

gofmt -l $(git ls-files '*.go')    # must be empty
go vet ./... && go build ./...

go test ./internal/config -run TestWhitelist    # a single unit test
go test ./features/...                          # the whole godog suite
```

`go test ./...` runs everything including `./features/...`; `make test`
deliberately excludes features so unit runs stay fast.

## Local run modes (MODE=live|demo|mock)

`internal/config` resolves a single `MODE` switch instead of raw URL overrides:

- `MODE=live` (image default) — `https://live.trading212.com/api/v0`. Requires
  `T212_API_KEY` (and `T212_API_SECRET` unless using a legacy key).
- `MODE=demo` — `https://demo.trading212.com/api/v0`, the paper-trading
  environment. Same credential rules.
- `MODE=mock` — points at `MOCK_URL` (default `http://localhost:8090`, must match
  the `mock` command's `--addr`) and supplies dummy credentials.

`.env.dist` ships `MODE=mock`, so `cp .env.dist .env` + `make run-mock` (terminal 1)
+ `make run` (terminal 2, needs an MQTT broker at `:1883`) runs the full pipeline
with no real credentials.

## Architecture

**CLI (`cmd/main.go` → `internal/cmd`).** Cobra root installs a `SIGTERM`/`SIGINT`
context and loads `.env` via godotenv. Subcommands: `serve` (the service), `mock`
(standalone fake API), `dump` (hidden diagnostic printing a raw API response to
stdout).

**Startup flow (`internal/cmd/serve.go`).** config → `trading212.New` → one
`account/summary` call to validate credentials and learn the account id and primary
currency → publish HA discovery → start `scheduler` → serve status/health.
Graceful shutdown publishes retained `offline` to every availability topic,
disconnects MQTT cleanly, then exits.

**`internal/trading212` — client and domain model.** Basic auth (legacy bare-key
fallback), the two GET endpoints, `x-ratelimit-*`-aware per-endpoint limiter, and
wire-to-domain parsing. `AccountSummary` and `Position` use pointer fields where
"absent" must differ from zero.

**The two-currency rule.** `averagePricePaid` and `currentPrice` are in the
instrument's currency; every `walletImpact` field is in the account's primary
currency. Entities carry the matching `unit_of_measurement`. Do not "simplify"
this.

**`internal/scheduler`.** Poll loop at `POLL_INTERVAL` (immediate first run) =
2 API calls → one `Snapshot` → one publish pass.

**MQTT/HA (`internal/mqtt`, `internal/homeassistant`, `internal/publisher`).** One
retained JSON state topic per device: the account device, plus one `via_device`
child per tracked ticker. Every entity lists the service availability topic; child
entities also list their own, with `availability_mode: all`, so an untracked or
unheld position greys out on its own. `Publisher` is an interface: autopaho in
prod, a recording fake in tests (no broker needed).

**Testing (`internal/mock`, `features`).** `internal/mock` serves the two endpoints
from canned payloads built from `Options`, and is shared by the `mock` subcommand
and the godog suite. `features/steps_test.go` drives the real `serve` wiring
against the mock and the recording `Publisher`, asserting topics, payloads,
availability transitions and readiness.

## Conventions

- **stdlib-first.** The only permitted external deps are cobra, autopaho
  (`paho.golang`), godotenv, and godog (test-only). Reach for stdlib (net/http,
  crypto, encoding/json, log/slog) before adding anything.
- **Read-only.** No code that places, modifies or cancels anything may exist. Pies
  endpoints are deprecated upstream and are not consumed.
- **No on-disk state.** Nothing is written to disk at runtime; the deployment is
  stateless and the container runs read-only. Retained MQTT messages are the only
  persistence.
- Structured logging via `log/slog`; config `String()` redacts secrets — never log
  credentials, and mask the account id to its last 4 digits in anything
  user-facing.
- **Conventional Commits**, one atomic commit per logical change. Never `chore`.
