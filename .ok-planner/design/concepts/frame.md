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

A frame is one cascade resolution. A row in `rimsky_frames` with `frame_resolution_mode IN ('coalesce','serial_queue')` and `state IN ('queued','running','completed','failed')`. Every dispatched run carries the frame it belongs to (`rimsky_node_runs.frame_id NOT NULL`; see CLAUDE.md "Frames are the unit of cascade resolution" gotcha). A frame begins when a node receives an invalidate (in-frame cascade walk) OR when pending boundary-crossing messages get delivered (see "Message delivery" below). It ends when no `rimsky_node_runs` row for the instance remains in state `stale` or `running` (post-2026-05-15 the predicate re-roots from `rimsky_nodes` to `rimsky_node_runs` since state lives on runs now).

**Message delivery as a frame-creation site** (post-2026-05-15). Boundary-crossing messages (operator-API enqueues, publisher-origin messages via `POST /instances/{id}/messages` with `sender_kind: "publisher"`) persist in `rimsky_messages` on receipt. At each frame boundary, undelivered messages for the instance are bundled per the per-instance `frame_delivery_mode` (`coalesce` default, `serial_queue` opt-in): rimsky walks subscriptions matching the envelope fields and stale-marks matching receivers within the new frame. The message's `delivered_at` and `frame_id` are populated. See `concept:message`, `runtime/message_delivery.go::DeliverPendingMessages`.

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

The two modes are illustrative of different authoring intents:

- **`serial_queue`** preserves ordering. Each invalidate produces its own frame; frames run one at a time per instance. Right answer when each invalidate carries distinct semantics that must be processed in order (e.g. "process item A, then process item B").
- **`coalesce`** preserves the latest input. While a frame is in flight, new invalidates merge into a single pending row; when the in-flight frame ends, that one merged row dispatches. Right answer when only the latest input matters (e.g. "recompute the dashboard from the current data"). Coalesce is **not** a debouncer — it merges all pending invalidates into one frame regardless of timing; it does not delay dispatch waiting for a quiet period.

The two modes never mix within an instance — the policy is template-level. `serial_queue` ordering is per-instance, not template-wide: two instances of the same template execute independently.

## Held frames

A frame is **held** when one or more of its node-runs is in a non-terminal pause state — typically `parked` (the node emitted `Park` waiting for a time-based or callback-based wake) but also `pending` claims awaiting acquisition. Held frames are surfaced via the `GET /admin/diagnostics/held-frames` route on the control API. They are normal during agent-driven work that includes external decisions; persistently held frames may indicate stuck reviews and warrant investigation. Held-claim auto-terminal fires once every node in the holding subgraph completes, so held-claim release happens at the end of the holding subgraph, not at the park boundary.

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


## Common pitfalls

- **Rimsky's frame is not a stack frame, video frame, or UI frame.** A Rimsky frame is the unit of cascade resolution for an instance; nothing to do with call stacks, animation, or screen rendering.
- Treating frame ID as a sequence number with strong ordering. It's a UUID; ordering across frames is captured by the timestamps of frame-start events, not by ID arithmetic.
- Assuming frames span instances. A frame is per-instance; two instances of the same template have entirely separate frame populations.
- Treating `coalesce` as a debouncer. Coalesce merges all pending invalidates into one frame regardless of timing; it does not delay dispatch waiting for a quiet period.
- Expecting `serial_queue` to give strong ordering across instances. The ordering guarantee is per-instance.

## Notes

- 2026-05-14: `rimsky_wait_set` rows are cascade-deleted on frame close via `ON DELETE CASCADE` from `rimsky_frames(frame_id)`. See `concept:wait-set`. Per spec `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.
- [2026-05-18] Folded content from former `docs/concepts/frame.md` (now retired) — held-frames exposition + serial-queue-vs-coalesce illustrative framing added to Purpose; stack-frame disambiguation + frame-ID / cross-instance pitfalls added as a Common-pitfalls subsection.
