---
tension: frame-lookup-on-every-enqueue
category: muddy-boundary
status: resolved
spec: 2026-06-14-message-schema-layer-design
affects:
  - frame
  - instance
resolution:
  summary: |
    Moot: the `frame_resolution_mode` template field retires. There is no
    template lookup on enqueue.
---

# `frame_resolution` and `frame_timeout_ms` are looked up via JOIN on every enqueue, but their values are template-hash-stable for the instance's lifetime

## What is muddy

`framesImpl.LookupFrameResolutionMode` (`foundation/persistence/postgres/frames.go`) executes a `JOIN rimsky_instances → rimsky_templates` query on every call to `frame.EnqueueOrCoalesce` (`graph/frame/producer.go`), reading `frame_resolution_mode` and `frame_timeout_ms` out of `rimsky_templates.spec` JSONB. The result is then used to route to `EnqueueSerialFrame` / `EnqueueCoalesceFrame` and stamp the new frame row.

But: `i.template_hash` is fixed at instance creation, the spec JSONB is content-addressed (`invariant adjacent to JCS canonicalization`), and there is no mechanism by which `frame_resolution_mode` or `frame_timeout_ms` could differ between two enqueues for the same instance. The lookup is doing real work to recompute a per-instance constant.

The same `LookupFrameResolutionMode` is the only code path that reads those fields, so the cost is contained, but the boundary between "spec-resident" (source of truth) and "instance-resident" (denormalized for read efficiency) is unclear: per-instance immutable values live in the spec, not on the instance row.

## Why it matters

Cold-read confusion: a reader tracking down "where is this instance's frame_resolution_mode stored?" follows a join into a JSONB column rather than finding it on `rimsky_instances`. Performance is not the issue (the join is indexed-PK on both sides), but the architectural shape — per-instance constants resident on the template — invites future drift if a "mutable override per instance" feature is added (cf. `userdata-overrides`, which *is* per-instance).

The competing pull is that denormalizing creates a "two sources of truth" problem if templates are ever re-registered with the same hash but a different observed spec (impossible by content-addressing) or if instance-level overrides are added later.

## Resolution

Moot: the `frame_resolution_mode` template field retires. There is no template lookup on enqueue.

## Evidence

- `_discover/2026-05-10-frame-resolution-model.md` Observations bullet "lookup-on-every-enqueue".
- `foundation/persistence/postgres/frames.go` — the JOIN.
- `graph/frame/producer.go` — the dispatch.
