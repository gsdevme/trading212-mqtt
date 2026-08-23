# 07 — Testing

## The mock's endpoint surface

`internal/mock` implements the same two endpoints the real API exposes:
`/api/v0/equity/account/summary` and `/api/v0/equity/positions` (`REQ-TS-01`). It
backs both the standalone `mock` subcommand (for exercising the full pipeline locally
with no credentials) and the godog acceptance suite.

**Refinement — payloads are built in Go, not read from fixture files.** The mock
**constructs its canned payloads in Go from an `Options` struct**, not by reading the
JSON fixtures under `internal/trading212/testdata/` at request time. `Options`
parameterises things like which positions are present, which are missing, and whether
a given endpoint should fail — so acceptance scenarios can vary server state (e.g.
"the positions endpoint returns an empty list" or "the summary endpoint returns
`500`") by constructing a different `Options` value per scenario, rather than by
swapping which fixture file is loaded. The design doc's original phrasing implied the
mock reads the `testdata/` fixtures directly; it does not — those fixtures exist
solely to back the `internal/trading212` parser's own unit tests, which need raw JSON
bytes to decode, not a live HTTP server.

## Fixture set

`internal/trading212/testdata/` holds raw JSON fixtures used by the parser's unit
tests. The set must include, at minimum, three edge cases (`REQ-TS-02`):

1. **A cross-currency holding** — an instrument whose `instrument.currency` differs
   from the account's `currency`, exercising the two-currency rule
   (`01-trading212-api.md`) by default rather than only when someone remembers to add
   it.
2. **A zero-cost position** — `walletImpact.totalCost` (or `investments.totalCost` for
   the account-level fixture) exactly `0`, exercising the `ReturnPct` zero-cost guard
   (`02-domain-model.md`, `REQ-DM-04`).
3. **A whitelisted ticker absent from the positions response** — proving the
   placeholder-discovery and offline-availability path
   (`03-mqtt-ha-discovery.md`, `REQ-HA-08`, `REQ-HA-09`) without needing a live
   account that happens to be missing a holding.

## Mock failure injection

The mock can be made to fail — return non-2xx statuses, malformed bodies, or time out
— so degraded behaviour (retry, backoff, skipped publish, readiness dropping) is
testable without needing the real API to misbehave on cue (`REQ-TS-03`).

## Unit-test coverage areas

- Parsing (wire DTO → domain type), against the `testdata/` fixtures.
- Config resolution, including `MODE` switching and every `TICKERS` mode (empty,
  list, `*`).
- The rate limiter's per-endpoint spacing and its `429`/`ResetAt` behaviour.
- Derived-field maths (`ReturnPct` for both `AccountSummary` and `Position`,
  including the zero-cost guard).
- Golden discovery payloads — the exact JSON published to each discovery topic,
  compared byte-for-byte against a checked-in golden file, so a change to the entity
  catalogue is a visible diff.

## The godog suite

`features/` drives the real `serve` wiring — the actual scheduler, publisher and
discovery code — against the mock server and the recording `Publisher`, with no
broker required (`REQ-TS-04`). It asserts on:

- Which topics get published, and in what order (discovery before state, account
  device before position devices).
- Payload contents, including currency fields on cross-currency fixtures.
- Availability transitions (`online`/`offline`) as positions appear, disappear and
  reappear across polls.
- Readiness behaviour, including the acceptance scenario proving readiness drops
  under sustained API failure — that scenario runs **one successful poll first**,
  then switches the mock to failing, and only then asserts `/readyz` reports
  not-ready. Asserting not-ready after nothing but failures would prove nothing,
  since `/readyz` starts not-ready before any publish has ever succeeded; the
  scenario has to first establish "ready", then break it, to demonstrate the
  threshold actually does something.

## `make test` / `make test-e2e`

`make test` runs unit and integration tests, **excluding** `features/`, so the
everyday test loop stays fast. `make test-e2e` runs the godog suite
(`REQ-TS-05`). `make lint` runs golangci-lint from a pinned binary in `./bin`.
