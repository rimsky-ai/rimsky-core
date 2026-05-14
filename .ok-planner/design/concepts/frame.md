---
concept: frame
status: as-is
aliases:
  - cascade-frame
references:
  - _discover/2026-05-10-frame-resolution-model.md
  - _discover/2026-05-10-frame-stuck-is-advisory.md
  - _discover/2026-05-10-cron-no-backfill.md
---

# Frame

## What it is

A frame is one cascade resolution. A row in `rimsky_frames` with `frame_resolution_mode IN ('coalesce','serial_queue')` and `state IN ('queued','running','completed','failed')`. Every dispatched run carries the frame it belongs to (`rimsky_node_runs.frame_id NOT NULL`; see CLAUDE.md "Frames are the unit of cascade resolution" gotcha). A frame begins when a node receives an invalidate and ends when no `rimsky_nodes` row for the instance remains in `stale` or `running`.

**Template-author surface.** `TemplateSpec.FrameResolutionMode` is a required top-level string field on the template (defined in `foundation/spec/template.go`, re-exported at `graph/node/template.go`), with two valid values declared as constants `FrameResolutionCoalesce` / `FrameResolutionSerialQueue` in `foundation/spec/template.go`. `validateFrameResolution` (`graph/node/template_validator.go`) rejects empty or unknown values at registration. A companion `FrameTimeoutMs` field is optional with default 600000 ms (10 min) and hard floor 60000 ms (60 s), enforced in `template_validator.go`.

**Template-time → runtime mapping.** `TemplateSpec.FrameResolutionMode` is JCS-canonicalized into `rimsky_templates.spec` JSONB at registration; it is *not* denormalized onto `rimsky_instances`. `framesImpl.LookupFrameResolutionMode` (`foundation/persistence/postgres/frames.go`) reads the mode + timeout fresh on every enqueue:

```sql
SELECT COALESCE(t.spec->>'frame_resolution_mode', '') AS frame_resolution_mode,
       COALESCE(NULLIF((t.spec->>'frame_timeout_ms'),'')::bigint, 600000) AS frame_timeout_ms
FROM rimsky_instances i
JOIN rimsky_templates  t ON t.id = i.template_hash
WHERE i.id = $1
```

`frame.EnqueueOrCoalesce` (`graph/frame/producer.go`) is the single producer-side entry point: it calls `LookupFrameResolutionMode` then routes to `EnqueueSerialFrame` (`frame_resolution_mode='serial_queue'`, always a fresh row) or `EnqueueCoalesceFrame` (`ON CONFLICT (instance_id) WHERE state='queued' AND frame_resolution_mode='coalesce' DO UPDATE` appending the source node to `source_node_ids[]`). The `frame_timeout_ms` value is stamped on the frame row at INSERT time, so spec edits affect future frames only.

## Purpose

Frames are the unit of cascade resolution. They let new invalidates that arrive during in-flight propagation be either serialized (`serial_queue`) or merged into a single pending update (`coalesce`), without preempting the running work. They also tie the audit trail together: every terminal handler attributes back to its frame.

## Boundaries

Owns: the per-instance concurrency rule (≤1 running frame), the coalesce/serial_queue policy, `last_progress_at`, frame_timeout warning emission, `frame: in | next` per-emit discipline. Does NOT own: node state (lives on `rimsky_nodes`), claim conflict (lives in `claim-handle`), scheduling cadence (lives in `schedule`). Adjacent: `cascade`, `node`, `node-run`, `invalidate`, `schedule`.

## Invariants

- At most one `state='running'` frame per instance (`uq_rimsky_frames_running` partial index).
- At most one `state='queued' AND mode='coalesce'` frame per instance (`uq_rimsky_frames_coalesce_queued`).
- `rimsky_node_runs.frame_id NOT NULL` — every dispatched row carries its frame (CLAUDE.md "Frames are the unit of cascade resolution" gotcha).
- Operator-originated invalidates do not preempt running work; they enqueue or coalesce.
- Frame mode is template-hash-stable per instance: `i.template_hash` is fixed at instance creation and the spec JSONB is content-addressed, so an instance's `frame_resolution_mode` cannot drift.
- `frame_timeout_ms` is purely advisory: when `last_progress_at` falls outside the window, the scheduler emits a single `frame.stuck.observed` slog warning and takes no destructive action. Hard floor 60s; default 600s.

## Aliases and historical names

`cascade-frame` is occasionally used in older sketches. Both the template-author surface (`frame_resolution_mode:`) and the persisted column (`rimsky_frames.frame_resolution_mode`) now share the same name — the post-2026-05-12 nomenclature resolution converged the two.

## Open within this concept

- The "advisory frame_timeout" vs "destructive park-timeout" sibling timeout asymmetry — see `tensions/timeout-policy-asymmetry.md`.
- Per-instance scope of `serial_queue` ordering may surprise readers expecting template-wide ordering — see `tensions/serial-queue-per-instance.md`.
- Template-author `frame_resolution:` vs runtime-column `mode` vocabulary mismatch — see `tensions/frame-resolution-vs-mode-vocabulary.md`.
- `LookupFrameResolutionMode` joins on every enqueue rather than denormalizing once per instance — see `tensions/frame-lookup-on-every-enqueue.md`.

