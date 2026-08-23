# 05 — Configuration

All configuration is environment-based. `godotenv` loads a local `.env` for
development; `.env.dist` ships a working `MODE=mock` default so the pipeline runs
with no credentials at all.

## Environment variables

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

## `MODE` resolution

`MODE` is a single switch rather than raw base-URL overrides, so there is exactly one
way to point the service at a non-live environment — no combination of environment
variables can accidentally leave the base URL and the intended environment
disagreeing with each other.

| `MODE` | Resolved base URL | Credentials |
|---|---|---|
| `live` | `https://live.trading212.com/api/v0` | `T212_API_KEY` required |
| `demo` | `https://demo.trading212.com/api/v0` | `T212_API_KEY` required |
| `mock` | `MOCK_URL` + `/api/v0`, default `http://localhost:8090/api/v0` | Dummy values supplied automatically |
| anything else | — | Validation error |

`T212_API_KEY` is required in `live` and `demo`; `mock` supplies dummy credentials so
the pipeline runs with nothing configured beyond a broker URL (`REQ-CF-01`,
`REQ-CF-02`).

`MQTT_BROKER_URL` is required in every mode, including `mock`, and must parse as a
valid URL with a non-empty scheme, e.g. `mqtt://host:1883` (`REQ-CF-03`). autopaho
needs a scheme regardless, so this rejects at startup what would otherwise fail
later at connect time.

## `TICKERS` semantics

- **Empty** publishes account entities only and creates no position devices — the
  safe default, since an account with hundreds of holdings would otherwise
  carpet-bomb Home Assistant with devices on first run.
- **A comma-separated list** (`AAPL_US_EQ,VUSA_EQ`) creates one device per listed
  ticker, at startup, whether or not the position is currently held.
- **The literal `*`** tracks whatever is held: devices are discovered from poll
  results, so a position opened while the service is running gets its discovery
  configs published at the poll that first sees it, and a position that disappears
  keeps its device but flips to `offline` availability.

With an explicit list, the set of devices is fixed for the process lifetime; with
`*`, it grows as holdings appear. `TICKERS` parses into a `Whitelist` (`REQ-CF-05`;
see `02-domain-model.md` for `Whitelist`'s casing rules).

## `POLL_INTERVAL` floor

`POLL_INTERVAL` defaults to `5m` and has a `1m` floor: any configured value below
one minute is rejected with a validation error, not silently raised to the floor
(`REQ-CF-04`). Clamping would hide an operator's mistake; rejecting it means
whoever typed `30s` finds out at startup, not by wondering why the account looks
throttled. This floor exists because Trading 212's rate limits are enforced **per account**, not
per key or per process — an over-eager `POLL_INTERVAL` doesn't just risk throttling
this service, it throttles every other tool sharing the same account's credentials,
including the human using the Trading 212 apps. A one-minute floor keeps the service
from becoming an inadvertently noisy neighbour to its own account.

## `READY_FAILURE_THRESHOLD`

Defaults to `3`; validation rejects anything below `1`, since a threshold of `0` would
make `/readyz` unready on the very first transient failure with no tolerance at all
(`REQ-CF-06`).

## Validation

Every configuration error found is collected via `errors.Join` rather than
short-circuiting on the first one, so a misconfigured deployment reports everything
wrong with it in one failed startup instead of one error per redeploy attempt
(`REQ-CF-07`). This includes malformed input, not just missing or out-of-range
values: an unparseable `POLL_MAX_RETRIES` or `READY_FAILURE_THRESHOLD` (e.g.
`not-a-number`) is reported by name rather than silently falling back to its
default.

## Redaction

`Config.String()` redacts `T212_API_KEY`, `T212_API_SECRET` and `MQTT_PASSWORD`
(`REQ-CF-08`). Neither credentials nor the full account id are ever logged; the
status page additionally masks the account id to its last four digits (see
`06-lifecycle-health.md`).
