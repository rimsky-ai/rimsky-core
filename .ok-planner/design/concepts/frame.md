---
concept: frame
status: as-is
aliases:
  - cascade-frame
---

# Frame

## Definition

A frame is one cascade resolution. It is a persisted frame row carrying a triggering-message reference and a lifecycle state drawn from the frame lifecycle-state family. Every dispatched run carries the frame it belongs to (the run row's frame reference is non-null). A frame begins only when a message lands in the message ledger and the next frame boundary picks it up — operator-emitted, publisher-emitted, or cascade-emitted by a message-emitter node, all converging on the same delivery path. Resuming a parked node — park-wake, via async callback or snooze timer — does not begin a frame; it resumes the still-running frame the parked node belongs to.

The frame ends when every node_run in the frame is in a terminal state (`fresh` or `failed` per `concept:node-run`). Any node_run in an in-flight state (`pending`, `stale`, `running`, `held`, `parked`) holds the frame open — `pending` (cascade-waiting), `stale` (awaiting dispatcher claim), `running` (in flight), `held` (awaiting auto-terminal commit/abandon), and `parked` (awaiting wake) all count uniformly as in-flight for frame-end purposes.

Frames are serial per instance: at most one running frame, queued frames dispatched in arrival order.

## Purpose

Frames are the unit of cascade resolution. They let new messages arriving during in-flight propagation queue cleanly without preempting the running work — at most one frame runs per instance, queued frames dispatch in arrival order. They also tie the audit trail together: every terminal handler attributes back to its frame, and every frame back to the triggering message.

Ordering is per-instance, not template-wide: two instances of the same template execute independently. A consumer expecting template-wide serialization must coordinate above rimsky.

## Held frames

A frame is **held** when one or more of its node-runs is in a non-terminal pause state — `parked` (executor entered the park terminal waiting for a time-based or callback-based wake) or `held` (executor returned a held-claim terminal awaiting auto-terminal commit/abandon per `concept:node-run`). Held frames are surfaced via a held-frames diagnostics endpoint on the control API. They are normal during agent-driven work that includes external decisions and during held-claim work that spans multi-node holding subgraphs; persistently held frames may indicate stuck reviews and warrant investigation. Held-claim auto-terminal fires once every node in the holding subgraph completes, so held-claim release happens at the end of the holding subgraph, not at the per-node held-terminal boundary. A held frame is precisely a running frame with a node_run in `held` or `parked` (or `pending` / `stale` waiting indefinitely on a slow upstream); because every in-flight state holds the frame open, the held-frames diagnostic and the frame-end rule agree.

## Boundaries

Owns: the per-instance concurrency rule (≤1 running frame), the serial-per-instance ordering, the last-progress timestamp, frame-timeout warning emission, the triggering-message-id pointer that every frame carries. Does NOT own: node state (lives on the node-run, see `concept:node-run`), claim conflict (lives in `concept:claim-handle`), scheduling cadence (lives in `concept:sensor`), the message itself (see `concept:message`). Adjacent: `concept:cascade`, `concept:node`, `concept:node-run`, `concept:message`, `concept:sensor`.

## Invariants

- At most one `running` frame per instance.
- Every frame row carries a non-null triggering-message reference. There is no path that creates a frame without a triggering message.
- Every dispatched run row carries a non-null frame reference.
- Frames are processed in arrival order per instance; cross-instance ordering is independent.
- The frame timeout is purely advisory: when the last-progress timestamp falls outside the window, the scheduler emits a single stuck-frame warning event and takes no destructive action.

## Common pitfalls

- **Rimsky's frame is not a stack frame, video frame, or UI frame.** A Rimsky frame is the unit of cascade resolution for an instance; nothing to do with call stacks, animation, or screen rendering.
- Treating frame ID as a sequence number with strong ordering. It's a UUID; ordering across frames is captured by the timestamps of frame-start events, not by ID arithmetic.
- Assuming frames span instances. A frame is per-instance; two instances of the same template have entirely separate frame populations.
