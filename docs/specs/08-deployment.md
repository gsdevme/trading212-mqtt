# 08 — Deployment

## Image

A multi-stage build: a Go build stage compiles a static binary, and a final
**distroless** stage carries only that binary and CA certificates — no shell, no
package manager, no OS userland to exploit or patch (`REQ-DP-01`). The container:

- Runs as a **non-root** user.
- Uses a **read-only root filesystem** — consistent with the no-on-disk-state
  principle (`06-lifecycle-health.md`, `REQ-LC-07`): if the process never needs to
  write to disk, the filesystem never needs to allow it.
- Mounts **no volumes**. There is nothing stateful for a volume to hold.

## Image defaults

`MODE` defaults to `live` in the image — the image is meant to be run in production
by default, with `MODE=mock`/`demo` as an explicit opt-in for local development or
staging, not the other way around. The image exposes port `8080`, matching the
default `HTTP_ADDR` (`REQ-DP-02`).

**Refinement — environment variable names.** Configuration into the container follows
the names defined in `05-config.md` and the design doc exactly: `TOPIC_PREFIX`,
`DISCOVERY_PREFIX`, `HTTP_ADDR` — not `MQTT_TOPIC_PREFIX`, `HA_DISCOVERY_PREFIX`,
`LISTEN_ADDR`, or any other naming convention a deployment manifest author might
otherwise reach for by habit. There is exactly one set of environment variable names
for this service, defined once.

## Probes

Kubernetes liveness probes `/healthz`; readiness probes `/readyz`. Both are described
in full in `06-lifecycle-health.md`. Probe timing (intervals, thresholds, initial
delay) is a manifest concern, not something this repository defines.

## No manifests in this repository

No Kubernetes manifests, Helm charts, Kustomize overlays or MQTT broker provisioning
of any kind live in this repository (`REQ-DP-03`). This repository produces exactly
one artifact for deployment purposes: the container image. Whatever runs it —
Deployment spec, resource limits, Secret wiring, broker address — is defined and
maintained in a separate repository outside this project's scope.

## CI

Every pull request runs lint, the unit/integration test suite (`make test`) and the
godog acceptance suite (`make test-e2e`) (`REQ-DP-04`).

## Releases

Releases are cut by **release-please**: conventional-commit history drives version
bumps and changelog generation, and a container image is built and pushed only when a
release is actually cut — not on every merge to the default branch (`REQ-DP-05`).
