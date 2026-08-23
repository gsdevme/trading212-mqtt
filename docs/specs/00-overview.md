# 00 — Overview

## Purpose

A small, stdlib-first Go service that polls the **Trading 212 Public API** and
republishes account and position metrics to **MQTT** with **Home Assistant (HA) MQTT
autodiscovery**, so a Trading 212 Invest / Stocks ISA account appears in Home Assistant
as a device with cash, value and profit-and-loss entities — plus one nested device per
whitelisted holding — with no manual HA configuration.

Deployed to Kubernetes as a **stateless** Deployment. Manifests live outside this repo.

## Principles

- **Spec-driven.** `docs/specs/*` are authored first and are the source of truth. Code
  is reconciled against `REQUIREMENTS.md` via `/spec-reconcile`. Behaviour changes
  update the spec and the relevant `REQ-*` entry in the same change, so the documents
  never drift silently out of sync with the tree.
- **stdlib-first.** Latest Go. The only external dependencies are cobra (CLI), autopaho
  (MQTT), godotenv (`.env`) and godog (BDD, test-only). HTTP, JSON, crypto, scheduling
  and logging all come from the standard library, keeping the dependency surface small
  and auditable.
- **Strictly read-only.** The service calls only `GET /equity/account/summary` and
  `GET /equity/positions`. No order placement, cancellation or pie mutation code exists
  anywhere in the tree. This is asserted by a requirement, not just by convention, so a
  future change that tries to add a write call fails traceability review.
- **No on-disk state.** Nothing is written to disk at runtime: no token cache, no
  spooled fixtures, no writable volume. The container runs non-root with a read-only
  root filesystem. Retained MQTT messages are the only persistence, held by the broker
  across pod restarts. `.env` is a local-development convenience only; in-cluster
  configuration comes from environment variables and Secrets.
- **Kube-friendly.** HTTP liveness and readiness probes, environment-based
  configuration, structured logging (`log/slog`), graceful shutdown and a distroless
  image make the service a natural fit for a Kubernetes Deployment without any
  bespoke operational tooling.

## High-level flow

1. Load and validate config.
2. Construct the `trading212.Client`.
3. Issue one `GET /equity/account/summary` to validate credentials and learn the
   account id and primary currency — both are prerequisites for discovery: the id is
   the device identifier, the currency is every monetary entity's unit.
4. Publish discovery for the account device and each whitelisted position device.
5. Start the scheduler.
6. Start the HTTP server.

## Non-goals

- Any form of trading, order management or pie mutation.
- Pies as a data source. The Pies API is formally deprecated upstream and is not
  consumed.
- Historical endpoints (orders, dividends, transactions, CSV exports).
- Multiple accounts per process. One process serves one Trading 212 account.
- Kubernetes manifests, Helm charts, or MQTT broker provisioning.

## See the sibling specs

- [`01-trading212-api.md`](01-trading212-api.md) — Environments, auth, the two
  endpoints, rate-limit headers, the two-currency rule.
- [`02-domain-model.md`](02-domain-model.md) — `AccountSummary`, `Position` and
  `Snapshot`, derived fields, optional-field and whitelist conventions.
- [`03-mqtt-ha-discovery.md`](03-mqtt-ha-discovery.md) — Topics, device blocks,
  `via_device`, the entity catalogue, availability.
- [`04-polling-scheduling.md`](04-polling-scheduling.md) — Poll loop, immediate first
  run, rate limiting, filtering.
- [`05-config.md`](05-config.md) — Environment variables, `MODE`, `TICKERS`,
  redaction.
- [`06-lifecycle-health.md`](06-lifecycle-health.md) — Probes, readiness rules,
  graceful shutdown, status page.
- [`07-testing.md`](07-testing.md) — Mock server, fixtures, unit tests, godog suite.
- [`08-deployment.md`](08-deployment.md) — Image, statelessness, probes, release
  process.
- [`REQUIREMENTS.md`](REQUIREMENTS.md) — The `REQ-*` traceability index.
