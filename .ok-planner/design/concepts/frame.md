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

A frame is one cascade resolution. A row in `rimsky_frames` with `mode IN ('coalesce','serial_queue')` and `state IN ('queued','running','completed','failed')`. Every dispatched run carries the frame it belongs to (`rimsky_worker_request.frame_id NOT NULL`; see CLAUDE.md "Frames are the unit of cascade resolution" gotcha). A frame begins when a node receives an invalidate and ends when no `rimsky_nodes` row for the instance remains in `stale` or `running`.

**Template-author surface.** `TemplateSpec.FrameResolution` is a required top-level string field on the template (`modeling/node/template.go:35`), with two valid values declared as constants `FrameResolutionCoalesce` / `FrameResolutionSerialQueue` at `modeling/node/template.go:44-45`. `validateFrameResolution` (`modeling/node/template_validator.go:166-181`) rejects empty or unknown values at registration. A companion `FrameTimeoutMs` field (`template.go:36`) is optional with default 600000 ms (10 min) and hard floor 60000 ms (60 s), enforced at `template_validator.go:183-188`.

**Template-time → runtime mapping.** `TemplateSpec.FrameResolution` is JCS-canonicalized into `rimsky_templates.spec` JSONB at registration; it is *not* denormalized onto `rimsky_instances`. `framesImpl.LookupFrameMode` (`foundation/persistence/postgres/frames.go:251-275`) reads the mode + timeout fresh on every enqueue:

```sql
SELECT COALESCE(t.spec->>'frame_resolution', '') AS mode,
       COALESCE(NULLIF((t.spec->>'frame_timeout_ms'),'')::bigint, 600000) AS frame_timeout_ms
FROM rimsky_instances i
JOIN rimsky_templates  t ON t.id = i.template_hash
WHERE i.id = $1
```

`frame.EnqueueOrCoalesce` (`modeling/frame/producer.go:26-43`) is the single producer-side entry point: it calls `LookupFrameMode` then routes to `EnqueueSerialFrame` (`mode='serial_queue'`, always a fresh row) or `EnqueueCoalesceFrame` (`ON CONFLICT (instance_id) WHERE state='queued' AND mode='coalesce' DO UPDATE` appending the source node to `source_node_ids[]`). The `frame_timeout_ms` value is stamped on the frame row at INSERT time, so spec edits affect future frames only.

## Purpose

Frames are the unit of cascade resolution. They let new invalidates that arrive during in-flight propagation be either serialized (`serial_queue`) or merged into a single pending update (`coalesce`), without preempting the running work. They also tie the audit trail together: every terminal handler attributes back to its frame.

## Boundaries

Owns: the per-instance concurrency rule (≤1 running frame), the coalesce/serial_queue policy, `last_progress_at`, frame_timeout warning emission, `frame: in | next` per-emit discipline. Does NOT own: node state (lives on `rimsky_nodes`), claim conflict (lives in `claim-handle`), scheduling cadence (lives in `schedule`). Adjacent: `cascade`, `node`, `worker-request`, `invalidate`, `schedule`.

## Invariants

- At most one `state='running'` frame per instance (`uq_rimsky_frames_running` partial index).
- At most one `state='queued' AND mode='coalesce'` frame per instance (`uq_rimsky_frames_coalesce_queued`).
- `rimsky_worker_request.frame_id NOT NULL` — every dispatched row carries its frame (CLAUDE.md "Frames are the unit of cascade resolution" gotcha).
- Operator-originated invalidates do not preempt running work; they enqueue or coalesce.
- Frame mode is template-hash-stable per instance: `i.template_hash` is fixed at instance creation and the spec JSONB is content-addressed, so an instance's `frame_resolution` cannot drift.
- `frame_timeout_ms` is purely advisory: when `last_progress_at` falls outside the window, the scheduler emits a single `frame.stuck.observed` slog warning and takes no destructive action. Hard floor 60s; default 600s.

## Aliases and historical names

`cascade-frame` is occasionally used in older sketches. The template-author surface uses `frame_resolution:`; the persisted column is `rimsky_frames.mode` — same data, two names across the same flow.

## Open within this concept

- The "advisory frame_timeout" vs "destructive park-timeout" sibling timeout asymmetry — see `tensions/timeout-policy-asymmetry.md`.
- Per-instance scope of `serial_queue` ordering may surprise readers expecting template-wide ordering — see `tensions/serial-queue-per-instance.md`.
- Template-author `frame_resolution:` vs runtime-column `mode` vocabulary mismatch — see `tensions/frame-resolution-vs-mode-vocabulary.md`.
- `LookupFrameMode` joins on every enqueue rather than denormalizing once per instance — see `tensions/frame-lookup-on-every-enqueue.md`.

