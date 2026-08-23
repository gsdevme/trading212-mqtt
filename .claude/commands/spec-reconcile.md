---
description: Reconcile docs/specs (REQUIREMENTS.md REQ-* IDs) against the codebase and report drift. Read-only — never edits code.
---

# /spec-reconcile

Reconcile the spec-driven requirements against the actual implementation and
produce a **drift report**. This command is **read-only**: it inspects and
reports; it must not modify code, specs, or tests.

## Inputs

- `docs/specs/REQUIREMENTS.md` — the authoritative list of `REQ-*` IDs, each with
  a one-line requirement and a `→ file` implementation pointer.
- The other `docs/specs/*.md` for the detail behind each requirement.
- The Go source tree (`internal/`, `cmd/`), `features/`, `Dockerfile`, `go.mod`,
  `.claude/`.

## Procedure

1. **Parse requirements.** Extract every `REQ-*` ID, its text, and its `→`
   target(s) from `REQUIREMENTS.md`.
2. **Locate implementations.** For each requirement, check the pointed-to file
   exists and contains code plausibly satisfying it — grep for the concrete
   tokens the requirement names (function, type, const, endpoint path, env var,
   MQTT topic, header). Note whether a corresponding test exists.
3. **Scan for untraceable code.** Walk the tree for significant units (exported
   types and functions, HTTP handlers, endpoint paths, env-var reads, MQTT
   topics) and check each maps to at least one `REQ-*`. Flag anything with no
   traceable requirement.
4. **Detect mismatches.** Where a requirement names a specific constant, path,
   header, default or behaviour, verify the code matches — `POLL_INTERVAL`
   default `5m`, the 1-minute floor, base URLs, `state_class: total` on monetary
   balances, `availability_mode: all` on position entities, price entities using
   the instrument currency.
5. **Check the read-only invariant.** Grep the whole tree for any write endpoint
   (`/orders`, `/pies`, `POST`, `DELETE` against the Trading 212 base URL). Any
   hit is a critical finding.

## Output — reconciliation report

Print a Markdown report with these sections (omit empty ones):

- **Summary** — counts: total requirements, implemented, missing, partial;
  untraceable code units; mismatches.
- **Critical** — read-only invariant violations, or on-disk writes at runtime.
- **Missing** — `REQ-*` with no plausible implementation, with the pointer and
  what was searched for.
- **Partial / untested** — implemented but no test, or only partly satisfied.
- **Untraceable code** — code units with no `REQ-*`; suggest either a new
  requirement or that the code is dead.
- **Mismatches** — requirement says X, code does Y, with `file:line`.
- **Verified** — a compact checklist of `REQ-*` confirmed implemented (+ test).

## Rules

- Do **not** edit code, specs, tests or config. Reporting only.
- Cite `file:line` for every finding so items are actionable.
- Prefer concrete grep evidence over assumptions. If uncertain, mark **partial**
  and say what to check manually rather than guessing.
- If `REQUIREMENTS.md` and the code disagree because of an intentional design
  change, recommend updating the spec — but leave the change to the human.
