# 04 — Polling & scheduling

## Poll loop

The scheduler runs its first poll **immediately** on start, then again every
`POLL_INTERVAL` (`REQ-SC-01`). This is deliberate: waiting a full interval before the
first publish would leave a freshly started (or restarted) pod with no state on the
retained topics — and, worse, no readiness — for up to `POLL_INTERVAL`, which is a
poor fit for a Kubernetes rollout that expects a new pod to become ready quickly.

## Exactly two API calls per poll

Every poll makes exactly two calls — `GET /equity/account/summary` and
`GET /equity/positions` — and produces exactly one `Snapshot` (`REQ-SC-02`; see
`02-domain-model.md` for `Snapshot`'s shape). No poll makes more calls than this
regardless of whitelist size, because filtering happens after the fetch, not before
it.

## Client-side whitelist filtering

`GET /equity/positions` accepts an optional `ticker` query parameter that would let
the server filter server-side. This project does not use it. Filtering happens
client-side, against the parsed `Whitelist`, after one unfiltered call
(`REQ-SC-06`). Reasons:

- **Cost.** One unfiltered call is cheaper against the 1 req/s positions limit than
  one call per whitelisted ticker — the difference matters once a whitelist has more
  than one entry, and is enforced against the same per-account budget as everything
  else hitting this account.
- **`*` needs the full list anyway.** Discovering newly opened positions when
  `TICKERS=*` requires seeing every held position every poll; a server-side filter
  by ticker cannot express "whatever is currently held" the way an unfiltered call
  followed by client-side pass-through can.
- **One request shape.** Every poll issues the identical two requests regardless of
  whitelist configuration, which keeps the rate limiter's per-endpoint bookkeeping
  independent of `TICKERS`.

## Retry policy

A transient fetch failure (see the error table in `01-trading212-api.md`) is retried
up to `POLL_MAX_RETRIES` times with exponential backoff starting at 1 second (1s, 2s,
4s, …) before the poll is finally counted as failed (`REQ-SC-03`).

A poll that exhausts its retries **skips the publish** entirely. Retained MQTT state
is left at its last known values rather than being overwritten with nulls or a
stale-looking zero — a subscriber sees the last good snapshot, not a misleading
"everything just went to zero" (`REQ-SC-04`).

## Rate limiting

`internal/trading212/ratelimit.go` observes the `x-ratelimit-*` headers on every
response (`REQ-T2-08`) and additionally enforces its own **per-endpoint minimum
spacing** on top of whatever the upstream burst allowance permits: 5 seconds between
calls to `/equity/account/summary`, 1 second between calls to `/equity/positions`
(`REQ-T2-07`). At the default 5-minute `POLL_INTERVAL` this spacing never actually
delays anything — it exists so that a shorter interval, a crash-restart loop, or a
manual `dump` invocation running alongside `serve` cannot throttle the account, since
the limit is per account and would otherwise also affect anything else the user runs
against the same credentials.

On a `429`, the limiter backs off until `x-ratelimit-reset` and retries once before
falling back to the ordinary transient-failure/retry handling above.

## Poll failures and readiness

Every poll outcome — success or failure, and which — is reported to the health
reporter (`REQ-SC-05`). `/readyz` tracks consecutive poll failures: after
`READY_FAILURE_THRESHOLD` in a row it reports not-ready, and a single successful poll
clears the counter back to zero. See `06-lifecycle-health.md` for the full readiness
state machine.

## Test seam for time

The scheduler's only test seam for time is its `After` function (matching
`time.After`'s signature), used to trigger each tick. There is no separate injectable
`Now`/clock seam: nothing in the scheduler branches on the current wall-clock time —
only on how long to wait before the next tick — so a `Now` seam would be dead
configuration with no test that could exercise it. Tests that need to control ticking
substitute `After`, not the clock.
