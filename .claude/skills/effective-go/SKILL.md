---
name: effective-go
description: Use when writing or reviewing idiomatic Go in this repo — package boundaries, accept-interfaces/return-structs, error wrapping, context propagation, concurrency, zero-value design, table tests.
---

# Effective Go (project conventions)

Idioms this codebase follows. When in doubt, prefer the smaller, more boring
option.

## Package and API design

- **Accept interfaces, return structs.** Constructors return a concrete
  `*Client`; functions take narrow interfaces (`Publisher`, `Fetcher`) so tests
  substitute fakes.
- Define interfaces **where they are consumed**, not where they are implemented.
  `Publisher` lives with the publisher package, not with the MQTT backend.
- Keep interfaces small — one to three methods. A one-method interface is often
  ideal.
- Exported identifiers get doc comments starting with the identifier name.
- Package names are short, lower-case, no underscores. The name is part of the
  API: `trading212.Client`, never `trading212.Trading212Client`.
- Every package gets a package comment saying what it is for and pointing at the
  spec file that governs it.

## Errors

- Wrap with `%w` and enough context to locate the failure:
  `fmt.Errorf("fetch positions: %w", err)`.
- Sentinel errors (`var ErrUnauthorized = errors.New(...)`) for conditions
  callers branch on; typed errors when the caller needs data from them.
- Compare with `errors.Is` / `errors.As`, never string matching.
- Collect independent validation failures with `errors.Join` so a bad config
  reports every problem at once rather than one per run.

## Context

- `context.Context` is the first parameter, named `ctx`, never stored in a
  struct.
- Every blocking operation selects on `ctx.Done()`.
- Check `ctx.Err()` before treating a failure as a real error — a cancelled
  context during shutdown is not a fault.

## Zero values and optionality

- Make the zero value useful where you can.
- Use pointer fields when "absent" must be distinguishable from "zero" — a
  return percentage of `nil` (not computable) is not the same as `0.0`.

## Concurrency

- Guard shared state with a mutex held for the shortest possible span; copy the
  values out and unlock before doing work with them.
- Never block a callback from a third-party library; hand off to a goroutine.
- A goroutine's lifetime must be owned by something — a `WaitGroup`, a done
  channel — so shutdown can wait for it.

## Testing

- Table-driven tests with named cases; `t.Run(name, ...)` per case.
- Inject `now func() time.Time` and sleep functions so time-dependent code is
  deterministic. Never `time.Sleep` in a test to wait for behaviour.
- Prefer testing observable behaviour through the package's exported API over
  reaching into internals.
- Golden-file tests for payload shapes (discovery JSON); regenerate
  deliberately, never blindly.
