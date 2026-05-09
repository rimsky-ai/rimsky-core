# `inline` blob backend

**Status:** default. **Multi-process safe:** yes (no shared state).

## What it is

The degenerate `BlobBackend`: never produces handles, all writes go
inline to the existing `data` / `parked_payload_inline` /
`payload_inline` columns. The attribute write path checks
`backend.Name() == "inline"` and short-circuits the spill check.

## When to use

- Most deployments. Until you have a concrete reason to spill (large
  attribute payloads, multi-MB park context, large named-event
  payloads), `inline` is the right default.

## Configuration

```yaml
persistence:
  blob:
    backend: inline
```

No sub-block fields apply. The `spill_threshold_bytes` field is
ignored (inline never spills regardless of value size).

## Limits

- The underlying columns are JSONB / BYTEA; very large payloads
  inflate `rimsky_*` row sizes and slow scans. For payloads in the
  10+ MB range, use `pg-largeobject` or `filesystem`.
- Rimsky does not enforce a hard cap. The Postgres limit on a
  single JSONB value (~1 GiB) applies; the practical performance
  cliff is much earlier.
