# 06 — Lifecycle & health

`internal/server` exposes three HTTP surfaces: `/healthz`, `/readyz` and `/` (a status
page). The root context comes from `signal.NotifyContext`.

## `/healthz`

Returns `200 OK` for as long as the process is running — liveness only, not a
statement about the API, the broker or recent poll outcomes. Its only failure mode is
the process being unresponsive or dead, which is exactly what a Kubernetes liveness
probe should restart on (`REQ-LC-01`).

## `/readyz`

Readiness answers a narrower question: is this instance currently doing useful work?

- **Not ready** from process start until the **first successful publish** completes.
  A pod that has just started has nothing on the retained topics yet from this
  instance's run and should not receive traffic implying otherwise.
- **Ready** after that first successful publish, and stays ready as long as
  consecutive poll failures stay under `READY_FAILURE_THRESHOLD`.
- **Not ready again** once `READY_FAILURE_THRESHOLD` consecutive poll failures have
  been recorded.
- **A single successful poll clears the counter** back to zero, returning to ready
  immediately (`REQ-LC-02`).

This is the same failure counter the scheduler reports poll outcomes to
(`04-polling-scheduling.md`, `REQ-SC-05`).

## Status page (`/`)

The status page is a small HTML page showing:

- Mode (`live`/`demo`/`mock`).
- The account id, **masked to its last four digits** (`REQ-LC-06`) — never the full
  id, and never credentials.
- Last poll time.
- Last error, if any.
- Current entity counts (account entities, position devices).

Deliberately **omitted**: the API key, the API secret, the MQTT password, the full
account id, and any raw API response body. The status page is a debugging aid, not a
diagnostic dump — `dump` (see `01-trading212-api.md`) exists for that, and even it
only ever writes to stdout, never to a page anyone could stumble across.

## Startup ordering

The HTTP listener starts **before** the first API call, so `/healthz` and `/readyz`
answer correctly for the entire startup sequence — including the window where
credentials are still being validated — rather than the process being unprobeable
during init (`REQ-LC-03`). `/readyz` correctly reports not-ready during this window
since no publish has succeeded yet.

`401`/`403` returned by the startup credential-validation call are fatal: the process
logs and exits non-zero rather than starting a scheduler that would only fail every
poll. Bad credentials should crash-loop visibly under Kubernetes rather than run
blind and silently unready forever (`REQ-LC-04`).

## Graceful shutdown

On `SIGTERM` or `SIGINT`:

1. Stop accepting new work; let any in-flight poll finish.
2. Publish retained `offline` to every availability topic — the service topic and
   every position topic.
3. Disconnect the MQTT client cleanly.
4. Exit `0`.

(`REQ-LC-05`.) This is what lets a rescheduled or rolling-updated pod leave Home
Assistant showing everything unavailable rather than stale-but-online during the gap
before its replacement's first successful poll.

## No on-disk state

Nothing this service does at runtime writes to disk: no cache, no token file, no
spooled fixture, no writable volume of any kind. Retained MQTT messages held by the
broker are the only state that outlives a single process (`REQ-LC-07`); see
`08-deployment.md` for the read-only-root-filesystem consequence of this.
