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
- A **holdings table** (`REQ-LC-08`): one row per currently-held position, giving
  its ticker, instrument name, value in the account currency, and whether it is
  tracked — i.e. whether it matched `TICKERS` and so has a Home Assistant device.
  Rows are ticker-sorted, matching the scheduler's `held=` log line
  (`04-polling-scheduling.md`, `REQ-SC-07`). The table is omitted entirely when
  nothing is held.
- Below it, the same tickers as a **paste-ready `TICKERS=…` line**. This is the
  point of the table: copy it into `.env` or the Deployment env, restart, and the
  positions become devices. Without it, configuring `TICKERS` is a chicken-and-egg
  problem, since the IDs are Trading 212's own and appear nowhere else.

The per-row `Tracked` flag uses the same `Whitelist.Includes` predicate the
publisher filters on, so the page cannot claim a device that was never published.
It is not the same figure as the tracked **count** in the row above: that counts
devices, which includes whitelisted tickers that are not currently held, while the
per-row flag is held ∩ whitelist. The two legitimately differ.

Deliberately **omitted**: the API key, the API secret, the MQTT password, the full
account id, and any raw API response body. The holdings table adds tickers, names
and per-position values — the same class of information as the account totals
already on the page — and nothing that identifies the account. The status page is a debugging aid, not a
diagnostic dump — `dump` (see `01-trading212-api.md`) exists for that, and even it
only ever writes to stdout, never to a page anyone could stumble across.

## Startup ordering

The HTTP listener starts **before** the first API call, so `/healthz` and `/readyz`
answer correctly for the entire startup sequence — including the window where
credentials are still being validated — rather than the process being unprobeable
during init (`REQ-LC-03`). `/readyz` correctly reports not-ready during this window
since no publish has succeeded yet.

A **failure of any kind** from the startup credential-validation call is fatal: the
process logs and exits non-zero rather than starting a scheduler that has no account
id or primary currency to publish under (`REQ-LC-04`). `401`/`403` is the case that
motivates it — bad credentials should crash-loop visibly under Kubernetes rather than
run blind and silently unready forever — but the same treatment is applied
deliberately to a transient failure such as an API outage or a DNS hiccup. Fail-fast
is the intent: a pod that starts and never becomes ready is a worse outcome than one
Kubernetes restarts with backoff, because the former reports a healthy Deployment and
the latter reports a visibly failing one.

The one exception is the root context already being cancelled. A `SIGTERM` arriving
mid-startup makes the in-flight call return `context.Canceled`; that is a normal
termination, not a fault, so the process shuts the status listener down and exits `0`
as `REQ-LC-05` requires. Nothing beyond discovery has been published at that point,
and the MQTT connection (if one exists) is torn down without a clean disconnect, so
the Last Will still marks the service offline.

## Graceful shutdown

On `SIGTERM` or `SIGINT`:

1. Stop accepting new work; let any in-flight poll finish.
2. Publish retained `offline` to every availability topic — the service topic and
   every position topic. **Every position is attempted and the service topic is
   written unconditionally**, even if one of the position publishes fails: step 3's
   clean disconnect suppresses the Last Will, and the publisher is already latched
   shut, so a service topic left retained-`online` here could never be corrected by
   anything. Home Assistant would show a dead service as healthy indefinitely, which
   is the worst kind of monitoring failure. Failures are aggregated and logged.
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
