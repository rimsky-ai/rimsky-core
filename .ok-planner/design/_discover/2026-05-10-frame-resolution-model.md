---
topic: frame-resolution-model
kind: schema
---

# Frames are the unit of cascade resolution; at most one running per instance; `serial_queue` vs `coalesce`

## Description

A graph's reactive cascade can be triggered repeatedly while it's still propagating. Rimsky models each cascade resolution as a **frame** (`docs/concepts/frame.md`): a frame begins when a node receives an invalidate and ends when no node remains in `stale` or `running` for the instance. The template's `frame_resolution:` field is the per-template policy for new invalidates that arrive while a frame is in flight.

**Template-author surface.** `TemplateSpec.FrameResolution` is a required string field on the top-level template (`modeling/node/template.go:35`). Two valid values: `"coalesce"` and `"serial_queue"` (constants at `modeling/node/template.go:44-45`). `validateFrameResolution` (`modeling/node/template_validator.go:166-181`) rejects empty or unknown values with a `frame_resolution is required` / `not a valid value` error at template registration. A companion `FrameTimeoutMs` field (`template.go:36`) is optional with default 600000 ms (10 min) and hard floor 60000 ms (60 s) — anything below the floor is rejected (`template_validator.go:183-188`).

**Runtime mechanism.** `rimsky_frames` (`foundation/persistence/postgres/migrations/002-frame-resolution.sql:13-25`) carries one row per cascade resolution:

- `mode IN ('coalesce', 'serial_queue')` — template-level policy, stamped at frame creation.
- `state IN ('queued', 'running', 'completed', 'failed')` — lifecycle.

**Template-time → runtime mapping.** The `TemplateSpec.FrameResolution` string is JCS-canonicalized into `rimsky_templates.spec`'s JSONB at registration; it is *not* denormalized into `rimsky_instances` or any per-instance column. At every invalidate-or-schedule-fire that needs a frame, `framesImpl.LookupFrameMode` (`foundation/persistence/postgres/frames.go:254-275`) reads the mode and timeout fresh:

```sql
SELECT COALESCE(t.spec->>'frame_resolution', '') AS mode,
       COALESCE(NULLIF((t.spec->>'frame_timeout_ms'),'')::bigint, 600000) AS frame_timeout_ms
FROM rimsky_instances i
JOIN rimsky_templates  t ON t.id = i.template_hash
WHERE i.id = $1
```

`frame.EnqueueOrCoalesce` (`modeling/frame/producer.go:26-43`) is the single producer-side entry point. It calls `LookupFrameMode`, then routes to `EnqueueSerialFrame` (which always INSERTs a fresh row with `mode='serial_queue'`, `state='queued'`, `frame_timeout_ms`) or `EnqueueCoalesceFrame` (which does an `ON CONFLICT (instance_id) WHERE state='queued' AND mode='coalesce' DO UPDATE` to append the source node into the pending row's `source_node_ids[]`). The `frame_timeout_ms` value is stamped onto the frame row at INSERT time (`foundation/persistence/postgres/frames.go:283-292, 303-323`) — a template-spec edit that changes the timeout takes effect on the next frame's creation, not on any in-flight frame. The `mode` flows the same way: a `frame_resolution` change in a re-registered template (new content-addressed hash) means only fresh instances bound to the new hash see the new mode.

The producer is template-hash-stable: because `i.template_hash` is fixed at instance creation (the frame_id-non-null neighborhood) and the spec JSONB is content-addressed, an instance's frame mode is stable across its lifetime. There is no "mode override" per instance — `modeling/frame/producer_test.go:50` documents this with a `spec = '{"frame_resolution":"..."}'` fixture pattern.

Two unique partial indexes enforce the concurrency rules:

- **`uq_rimsky_frames_running`** (line 31) — at most one frame per instance is `state='running'`.
- **`uq_rimsky_frames_coalesce_queued`** (line 35) — at most one frame per instance is `state='queued' AND mode='coalesce'`.

`rimsky_worker_request.frame_id` is NOT NULL (the frame-id-non-null rule at `foundation/persistence/worker_requests.go:34`). Every dispatched run carries the frame it belongs to, so async terminal handlers can attribute their results correctly. `rimsky_nodes.frame_id` is nullable (line 55) because only nodes currently participating in the active cascade carry it.

Migration 004 adds `rimsky_frames.last_progress_at`, refreshed by every node-state transition (`foundation/integration/runner_terminal_handlers.go`). The scheduler tick reads `last_progress_at` for the stuck-frame warning; the warning is advisory only (`2026-05-10-frame-stuck-is-advisory`).

**`serial_queue`** preserves ordering. Each invalidate produces its own frame; frames run one at a time per instance. Right answer when each invalidate carries distinct semantics that must be processed in order (e.g. "process item A, then process item B"). `docs/concepts/frame.md` is explicit: under `serial_queue`, frames execute in arrival order per instance.

**`coalesce`** preserves the latest input. While a frame is in flight, new invalidates merge into a single pending row; when the in-flight frame ends, that one merged row dispatches. Right answer when only the latest input matters (e.g. "recompute the dashboard from the current data"). The single-row constraint is enforced by `uq_rimsky_frames_coalesce_queued`.

Operator-originated invalidates never preempt running work (`foundation/integration/supervisor.go:16-18`, also documented in `docs/concepts/frame.md` and CLAUDE.md): in both modes, the running frame runs to its terminal state, and the operator's invalidate either coalesces into the queued row or enqueues a new one.

Frame-end is the SQL predicate "no `rimsky_nodes` rows in `stale` or `running` for this instance" (`docs/concepts/frame.md`). At most one frame is `running` per instance, by construction. The scheduler tick checks this predicate as part of resolving the active frame.

The `frame: in | next` per-emit discipline (per `docs/concepts/invalidate.md`) controls whether a handler-emitted invalidate joins the source's current frame or buffers a new one. Default is `next`. The per-emit discipline applies to operator-API invalidate, error-types policy invalidate, and lifecycle-handler invalidate; cascade-on-commit and pure-cascade walks are scheduler actions and aren't configurable.

## Code surface

- `modeling/node/template.go:31-48` — `TemplateSpec.FrameResolution` / `FrameTimeoutMs` fields + the two constants + the timeout default (600000) and hard floor (60000).
- `modeling/node/template_validator.go:163-189` — `validateFrameResolution` (required, enum, timeout floor).
- `foundation/persistence/postgres/migrations/002-frame-resolution.sql` — schema (`rimsky_frames`, partial indexes).
- `foundation/persistence/postgres/migrations/004-last-outcome-and-progress.sql` — `last_progress_at`.
- `foundation/persistence/frames.go:31-36,152-155` — `FrameMode` type, constants, `LookupFrameMode` interface.
- `foundation/persistence/postgres/frames.go:251-275` — `LookupFrameMode` reads from `rimsky_templates.spec` JSONB via instance join.
- `foundation/persistence/postgres/frames.go:278-323` — `EnqueueSerialFrame` / `EnqueueCoalesceFrame` SQL bodies.
- `modeling/frame/producer.go:26-43` — `EnqueueOrCoalesce` dispatch on mode.
- `foundation/persistence/worker_requests.go:28-50` — `frame_id NOT NULL` rule.
- `foundation/integration/cascade_invalidate.go` — frame-aware cascade walks.
- `foundation/integration/cascade_recalculate.go` — pure-cascade walks.

## Prose surface

- `docs/concepts/frame.md` — concept-doc; serial_queue/coalesce, frame-end predicate, frame_timeout_ms.
- `docs/concepts/invalidate.md` — `frame: in | next` per-emit discipline.
- `docs/concepts/cascade.md` — cascade always happens in a frame.
- `CLAUDE.md` "Non-obvious gotchas" — operator invalidates do not preempt running work.
- `CLAUDE.md` "Blessed invariants" §19 — frame_id NOT NULL.

## Adjacent topics

- `2026-05-10-frame-stuck-is-advisory` — `last_progress_at` warning is non-destructive.
- `2026-05-10-cascade-fires-on-last-outcome` — `last_progress_at` refreshes per state transition.
- `2026-05-10-cron-no-backfill` — schedule fires create frames; no backfill.

## Observations

- The two modes never mix within an instance — the policy is template-level. A template that wants both behaviors (some invalidates serialized, others coalesced) cannot express this in `frame_resolution:`. The workaround is to split into two templates or use the per-emit `frame: in | next` discipline to selectively bypass the queue.
- `serial_queue` ordering is per-instance only; two instances of the same template execute independently (`docs/concepts/frame.md` "Common mistakes"). A reader who expects template-wide ordering will be surprised.
- The frame-end predicate (`no nodes in stale or running for this instance`) is evaluated at scheduler-tick frequency, not synchronously at every state transition. A frame whose last node just terminated technically isn't `resolved` until the next tick observes the empty state. The window is sub-second in practice but non-zero.
- `frame_timeout_ms` has a hard floor of 60_000 ms (60s) and a default of 600_000 (10min); these are operator-facing thresholds, not invariants. CLAUDE.md "Non-obvious gotchas" makes clear the timeout is advisory.
- **Tension candidate (lookup-on-every-enqueue):** `LookupFrameMode` does a fresh `JOIN rimsky_instances → rimsky_templates` on every invalidate emit. The template's frame_resolution is stable for the instance's lifetime (template-hash binding); a per-instance denormalization at instance create-time would let the producer skip the join. Cold-read tradeoff: the JOIN keeps the source of truth in the template spec without duplication; the cost is one indexed PK lookup per fire.
- **Tension candidate (template-time vs runtime vocabulary):** the field is `frame_resolution:` in template YAML but the persisted column is `mode`. The runtime concept doc uses "mode" while the template doc uses "frame_resolution." Two names for one thing across the same data flow is a cold-read friction point — a reader who greps for `frame_resolution` won't find the runtime column.
- **Tension candidate (no migration-time backfill for FrameTimeoutMs default):** `LookupFrameMode` returns `600000` when the spec's `frame_timeout_ms` is empty (`postgres/frames.go:261`). The template validator allows omission. The default is implicit in two places — the SQL `COALESCE` and the Go constant `FrameTimeoutDefaultMs` (`template.go:46`). A future change to the default needs both touched in sync.
