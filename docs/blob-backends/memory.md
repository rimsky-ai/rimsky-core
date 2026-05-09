# `memory` blob backend

**Multi-process safe:** **NO**. Dev-only. Rejected at startup unless
`RIMSKY_PROCESS_ROLE=unified` (set automatically by the
`rimsky-entrypoint` unified binary).

## What it is

In-process `map[Handle][]byte` plus `sync.RWMutex`. Handles formatted
as `mem:<n>`. Suitable for the unified-image dev/test setup and for
unit tests that exercise the blob interface without spinning up a
container.

## When to use

- Dev iteration with `rimsky-entrypoint` (the unified single-process
  binary).
- Unit / scenario tests in the rimsky repo.
- Demo / smoke runs where the lifetime of the data is the lifetime
  of the process.

## When NOT to use

- Any multi-process deployment. The per-process binaries
  (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`)
  cannot share state through an in-process map. The startup
  validator (`ValidateBlobConfig`) refuses to construct the backend
  unless `RIMSKY_PROCESS_ROLE=unified`.

## Configuration

```yaml
persistence:
  blob:
    backend: memory
```

No sub-block fields. The `spill_threshold_bytes` field still applies;
payloads at or below it stay inline.

## Operational notes

- All bytes are lost on process restart. Held claims that referenced
  spilled payloads will fail to read on resume — only safe when the
  whole deployment is single-process.
- Memory pressure: the backend holds bytes verbatim; large payloads
  bloat the rimsky process's RSS.
