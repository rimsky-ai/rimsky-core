---
concept: frame
aliases:
  - cascade-frame
---

# Frame

## What it is

A frame is one cascade resolution. It names the message that triggered it and the root run-scope it owns, and it carries a lifecycle state — running, completed, failed, or terminated — read from the runs the frame owns together with an end mark. The frame engine stamps that mark when the frame settles, and administrative instance termination stamps it inside the terminating transaction itself, so a killed frame is terminal at once rather than at the next scheduler tick. A frame owns a tree of run-scopes rooted at its root run-scope: every run in the frame lives in some run-scope under that root (see `concept:run-scope`), and every dispatched run names the frame it belongs to.

A frame begins only when a message sits pending on the instance's message queue and the frame engine picks it up on a tick. An operator-sent message, a publisher-sent message, and a message a message-sender node sends during a cascade all converge on that one pickup path. At pickup the runtime creates the root run-scope and the frame in one transaction, and the frame runs from that moment: frames have no pre-run state. Waking a parked node at its resume time begins no frame; it resumes the frame the parked node already belongs to.

The frame ends when every run in it has reached a terminal state (see `concept:node-run`). Settlement closes the frame's root run-scope in the same transaction that stamps the end mark, and stages the run-scope-terminal lifecycle deliveries for every scope the settlement closes there (see `concept:run-scope`, `concept:lifecycle-subscriber`); closing the root is a settlement act, not something instance teardown does later. The three terminal values record three different endings: completed says the cascade ran to quiescence, failed says the frame's own cascade broke, and terminated says administrative instance termination interrupted the frame. Termination force-fails the frame's in-flight runs under the instance-kill reason (see `concept:transition-reason`), so an interruption never reads as a genuine cascade failure. Any run that has not reached a terminal state holds the frame open, whatever that run waits on. A frame holding a parked or held run is a held frame: it still runs, and it waits on a time-wake or on an auto-terminal commit or abandon.

Frames are serial per instance: at most one frame runs at any moment, and while one runs the instance's message queue accumulates newly arriving messages for the frame engine to pick up next. Work waiting for a busy instance sits on that queue (see `concept:instance`), not at the frame layer.

Frames run in isolation from one another, and the isolation is structural (see `decision:frame-isolation-is-structural`). A message is the only carrier across a frame boundary: a frame's work may send a message whose envelope lands on the instance's message queue and triggers some later frame (see `concept:message-sender-node`), and a frame's work may read the payload of its own triggering message plus whatever the frame itself produces. Everything else the platform has persisted — the runs and attributes of earlier frames, the instance's node identities, the event log — exists for observability, not for a running frame to read back into a decision.

A frame is not a stack frame, a video frame, or a screen frame; it is the unit of cascade resolution for one instance. A frame belongs to exactly one instance, so two instances of one template have wholly separate frames. A frame's identity is a name, not a sequence number, and carries no ordering of its own.

## Purpose

A frame is the unit of cascade resolution. It lets messages that arrive while propagation is in flight accumulate on the instance's message queue without preempting the running work: one frame runs per instance, and the frame engine opens the next one when the current one settles. It also ties the audit trail together, because every terminal handler attributes back to its frame and every frame back to its triggering message. Ordering is per instance, not per template: two instances of one template run independently, and a consumer that needs template-wide serialization coordinates it above rimsky.

## Boundaries

A frame owns the per-instance concurrency rule that at most one frame runs, the serial-per-instance ordering, the last-progress mark that stall detection reads, the pointer to its triggering message, and the pointer to the root run-scope created when the frame starts.

A frame does not own node state, which lives on the run (see `concept:node-run`); claim conflict (see `concept:claim-handle`); scheduling cadence (see `concept:sensor`); the message itself (see `concept:message`); the message queue and its coalesce mode, which live on the instance (see `concept:instance`); or run-scope internals (see `concept:run-scope`). See also `concept:cascade` and `concept:node`.

## Aliases

- cascade-frame
