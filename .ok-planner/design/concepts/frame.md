---
concept: frame
status: as-is
aliases:
  - cascade-frame
---

# Frame

## Definition

A frame is one cascade resolution. It is a persisted frame row carrying a triggering-message reference and a lifecycle state (`queued`, `running`, `completed`, or `failed`). Every dispatched run carries the frame it belongs to (the run row's frame reference is non-null). A frame begins only when a message lands in the message ledger and the next frame boundary picks it up — operator-emitted, publisher-emitted, or cascade-emitted by a message-emitter node, all converging on the same delivery path. Resuming a parked node — park-wake, via async callback or snooze timer — does not begin a frame; it resumes the still-running frame the parked node belongs to. The frame ends when every node_run in the frame is resolved; a `parked` node_run holds its frame open.

Frames are serial per instance: at most one running frame, queued frames dispatched in arrival order.

## Purpose

Frames are the unit of cascade resolution. They let new messages arriving during in-flight propagation queue cleanly without preempting the running work — at most one frame runs per instance, queued frames dispatch in arrival order. They also tie the audit trail together: every terminal handler attributes back to its frame, and every frame back to the triggering message.

Ordering is per-instance, not template-wide: two instances of the same template execute independently. A consumer expecting template-wide serialization must coordinate above rimsky.

## Held frames

A frame is **held** when one or more of its node-runs is in a non-terminal pause state — typically `parked` (the node entered the park terminal waiting for a time-based or callback-based wake) but also `pending` claims awaiting acquisition. Held frames are surfaced via a held-frames diagnostics endpoint on the control API. They are normal during agent-driven work that includes external decisions; persistently held frames may indicate stuck reviews and warrant investigation. Held-claim auto-terminal fires once every node in the holding subgraph completes, so held-claim release happens at the end of the holding subgraph, not at the park boundary. A held frame is precisely a running frame with a `parked` (or acquisition-pending) node_run; because a parked node_run holds its frame open, the held-frames diagnostic and the frame-end rule agree.

## Boundaries

Owns: the per-instance concurrency rule (≤1 running frame), the serial-per-instance ordering, the last-progress timestamp, frame-timeout warning emission, the triggering-message-id pointer that every frame carries. Does NOT own: node state (lives on the node-run, see `concept:node-run`), claim conflict (lives in `concept:claim-handle`), scheduling cadence (lives in `concept:sensor`), the message itself (see `concept:message`). Adjacent: `concept:cascade`, `concept:node`, `concept:node-run`, `concept:message`, `concept:sensor`.

## Invariants

- At most one `running` frame per instance.
- Every frame row carries a non-null triggering-message reference. There is no path that creates a frame without a triggering message.
- Every dispatched run row carries a non-null frame reference.
- Frames are processed in arrival order per instance; cross-instance ordering is independent.
- The frame timeout is purely advisory: when the last-progress timestamp falls outside the window, the scheduler emits a single `frame.stuck.observed` warning and takes no destructive action.

## Common pitfalls

- **Rimsky's frame is not a stack frame, video frame, or UI frame.** A Rimsky frame is the unit of cascade resolution for an instance; nothing to do with call stacks, animation, or screen rendering.
- Treating frame ID as a sequence number with strong ordering. It's a UUID; ordering across frames is captured by the timestamps of frame-start events, not by ID arithmetic.
- Assuming frames span instances. A frame is per-instance; two instances of the same template have entirely separate frame populations.
