---
name: go-1.26
description: Use when writing Go for this repo to apply current-toolchain (go1.26) idioms — iterators, slices/maps helpers, log/slog, testing/synctest, t.Context, generics. Validated against the installed go1.26.2.
---

# Go 1.26 toolchain idioms

Validated against `go1.26.2`. Prefer these over older patterns.

## Iterators (range-over-func)

Functions returning `iter.Seq[T]` / `iter.Seq2[K,V]` are directly rangeable.
Bridge to slices with `slices.Collect`, `slices.Sorted`, and iterate maps with
`maps.Keys` / `maps.Values` (both return iterators — wrap them).

## `slices` and `maps`

`slices.Contains`, `ContainsFunc`, `IndexFunc`, `SortFunc`, `slices.Clone`,
`maps.Clone`, `maps.Equal`. Reach for these before hand-rolling loops.

## Structured logging

`log/slog` only. Prefer `logger.InfoContext(ctx, "msg", "key", value)` over
formatted strings so fields stay queryable. Build the handler once
(`slog.NewJSONHandler` / `NewTextHandler`) and pass the logger down.

## Testing

- `t.Context()` gives a context cancelled at test cleanup.
- `testing/synctest` runs goroutines on a fake clock — use it for scheduler and
  rate-limiter tests instead of real sleeps.
- `t.Cleanup` over `defer` when the cleanup must run after subtests.

## HTTP routing

`net/http`'s `ServeMux` supports method and wildcard patterns:
`mux.HandleFunc("GET /api/v0/equity/positions", h)` and `GET /{$}` to match the
exact root. No third-party router is needed.

## Misc

- `min` / `max` builtins.
- `errors.Join` for multi-error aggregation.
- `cmp.Or` for first-non-zero selection.
