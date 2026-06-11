---
concept: frame
status: as-is
aliases:
  - cascade-frame
---

# Frame

## What it is

A frame is one cascade resolution. It is a persisted frame row carrying a resolution mode (`coalesce` or `serial_queue`) and a lifecycle state (`queued`, `running`, `completed`, or `failed`). Every dispatched run carries the frame it belongs to (the run row's frame reference is non-null). A frame *begins* only when a node is invalidated — a direct operator/user invalidation, or message delivery (see Message delivery below). Resuming a parked node — park-wake, via async callback or snooze timer — does not begin a frame; it resumes the still-running frame the parked node belongs to. 'Begins a frame' is distinct from 'causes a frame to execute.' It ends only when every node_run in the frame is resolved — no node_run remains `stale`, `running`, or `parked`. A `parked` node_run holds its frame open; the frame does not end while any node is parked.

**Message delivery as a frame-creation site.** Boundary-crossing messages (operator-API enqueues, publisher-origin messages with `sender_kind: "publisher"`) persist in the message ledger on receipt. At each frame boundary, undelivered messages for the instance are bundled per the per-instance `frame_delivery_mode` (`serial_queue` default, `coalesce` opt-in — owned by `concept:message`; distinct from this concept's own `frame_resolution_mode`): rimsky walks subscriptions matching the envelope fields and stale-marks matching receivers within the new frame. The message's delivered-at and frame reference are populated. See `concept:message`.

**Template-author surface.** The frame-resolution-mode is a required top-level string field on the template, with two valid values (`coalesce` / `serial_queue`); the template validator rejects empty or unknown values at registration. A companion frame-timeout field is optional with default 600000 ms (10 min) and a hard floor of 60000 ms (60 s), enforced by the same validator.

**Template-time → runtime mapping.** The frame-resolution-mode is JCS-canonicalized into the template's content-addressed spec at registration; it is *not* denormalized onto the instance row. The frame producer reads the mode + timeout fresh on every enqueue, joining the instance to its template spec.

The frame producer is the single producer-side entry point: it looks up the mode, then routes to a serial-queue enqueue (always a fresh row) or a coalesce enqueue (which, on conflict with an already-queued coalesce frame for the same instance, updates that row in place, appending the source node to the frame's source-node list). The frame-timeout value is stamped on the frame row at insert time, so spec edits affect future frames only.

## Purpose

Frames are the unit of cascade resolution. They let new invalidates that arrive during in-flight propagation be either serialized (`serial_queue`) or merged into a single pending update (`coalesce`), without preempting the running work. They also tie the audit trail together: every terminal handler attributes back to its frame.

The two modes are illustrative of different authoring intents:

- **`serial_queue`** preserves ordering. Each boundary-crossing invalidate (operator-API send or publisher-origin message) produces its own frame; cascade walks stay within the current frame. Frames run one at a time per instance. Right answer when each invalidate carries distinct semantics that must be processed in order (e.g. "process item A, then process item B").
- **`coalesce`** preserves the latest input. While a frame is in flight, new invalidates merge into a single pending row; when the in-flight frame ends, that one merged row dispatches. Right answer when only the latest input matters (e.g. "recompute the dashboard from the current data"). Coalesce is **not** a debouncer — it merges all pending invalidates into one frame regardless of timing; it does not delay dispatch waiting for a quiet period.

The two modes never mix within an instance — the policy is template-level. `serial_queue` ordering is per-instance, not template-wide: two instances of the same template execute independently.

## Held frames

A frame is **held** when one or more of its node-runs is in a non-terminal pause state — typically `parked` (the node entered the park terminal waiting for a time-based or callback-based wake) but also `pending` claims awaiting acquisition. Held frames are surfaced via a held-frames diagnostics endpoint on the control API. They are normal during agent-driven work that includes external decisions; persistently held frames may indicate stuck reviews and warrant investigation. Held-claim auto-terminal fires once every node in the holding subgraph completes, so held-claim release happens at the end of the holding subgraph, not at the park boundary. A held frame is precisely a running frame with a `parked` (or acquisition-pending) node_run; because a parked node_run holds its frame open, the held-frames diagnostic and the frame-end rule agree.

## Boundaries

Owns: the per-instance concurrency rule (≤1 running frame), the coalesce/serial_queue policy, the last-progress timestamp, frame-timeout warning emission, `frame: in | next` per-emit discipline. Does NOT own: node state (lives on the node-run, see `node-run`), claim conflict (lives in `claim-handle`), scheduling cadence (lives in `sensor`). Adjacent: `cascade`, `node`, `node-run`, `invalidate`, `sensor`.

## Invariants

- At most one `running` frame per instance (enforced by a partial uniqueness index).
- At most one `queued` `coalesce` frame per instance (enforced by a partial uniqueness index).
- Every dispatched run row carries a non-null frame reference.
- Operator-originated invalidates do not preempt running work; they enqueue or coalesce.
- Frame mode is template-hash-stable per instance: the instance's template hash is fixed at creation and the spec is content-addressed, so an instance's frame-resolution-mode cannot drift.
- The frame timeout is purely advisory: when the last-progress timestamp falls outside the window, the scheduler emits a single `frame.stuck.observed` warning and takes no destructive action. Hard floor 60s; default 600s.

## Common pitfalls

- **Rimsky's frame is not a stack frame, video frame, or UI frame.** A Rimsky frame is the unit of cascade resolution for an instance; nothing to do with call stacks, animation, or screen rendering.
- Treating frame ID as a sequence number with strong ordering. It's a UUID; ordering across frames is captured by the timestamps of frame-start events, not by ID arithmetic.
- Assuming frames span instances. A frame is per-instance; two instances of the same template have entirely separate frame populations.
- Treating `coalesce` as a debouncer. Coalesce merges all pending invalidates into one frame regardless of timing; it does not delay dispatch waiting for a quiet period.
- Expecting `serial_queue` to give strong ordering across instances. The ordering guarantee is per-instance.
