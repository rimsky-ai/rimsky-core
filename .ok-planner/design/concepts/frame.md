---
concept: frame
status: as-is
aliases:
  - cascade-frame
---

# Frame

## Definition

A frame is one cascade resolution. It is a persisted frame row carrying a triggering-message reference, a non-null root RunScope reference, and a lifecycle state drawn from the frame lifecycle-state family. A frame owns a tree of RunScopes rooted at the frame's root RunScope; every node-run in the frame lives in some RunScope under that root (per `concept:run-scope`), and every dispatched run carries the frame it belongs to (the run row's frame reference is non-null). A frame begins only when a message sits pending in the instance's message queue and the frame engine picks it up on a tick — operator-emitted, publisher-emitted, or cascade-emitted by a message-emitter node, all converging on the same pickup path. At the pickup moment the runtime creates the root RunScope and the frame row in one tx, straight into the `running` state; frames have no pre-run state. Resuming a parked node — park-wake, via async callback or snooze timer — does not begin a frame; it resumes the still-running frame the parked node belongs to.

The frame ends when every node_run in the frame is in a terminal state (`fresh` or `failed` per `concept:node-run`). Any node_run in an in-flight state (`pending`, `stale`, `running`, `held`, `parked`) holds the frame open — `pending` (cascade-waiting), `stale` (awaiting dispatcher claim), `running` (in flight), `held` (awaiting auto-terminal commit/abandon), and `parked` (awaiting wake) all count uniformly as in-flight for frame-end purposes.

Frames are serial per instance: at most one running frame at any moment, and while one is running the instance's message queue accumulates any newly-arriving messages for the frame engine to pick up next. The waiting-work-for-a-slow-instance surface lives at the message queue (per `concept:instance`), not at the frame layer.

Frames run in perfect isolation from one another. The isolation is structural: no node-run, no RunScope, no attribute row, no wait-set row, no signal-emission decision, no gate evaluation, no cascade-walker step, and no substitution lookup performed inside a frame's work observes data that belongs to any other frame. The only cross-frame carrier is a **message**: a frame's work may emit a message (via `concept:message-emitter-node`) whose envelope lands on the instance's message queue and becomes the triggering message of some later frame; and the frame's own work may read only the payload of its own triggering message plus data produced inside the frame itself. Everything else — the persisted `rimsky_node_runs` and `rimsky_node_attributes` rows from prior frames, the persisted `rimsky_nodes` identity rows (which are immutable after instance creation per `concept:node`), the audit-event ledger — is invisible to the running frame's decisions. It exists on disk for external observability, not for the runtime to read back into a frame.

## Purpose

Frames are the unit of cascade resolution. They let new messages arriving during in-flight propagation accumulate cleanly on the instance's message queue without preempting the running work — at most one frame runs per instance, and the frame engine opens the next frame when the current one settles. They also tie the audit trail together: every terminal handler attributes back to its frame, and every frame back to the triggering message.

Ordering is per-instance, not template-wide: two instances of the same template execute independently. A consumer expecting template-wide serialization must coordinate above rimsky.

## Held frames

A frame is **held** when one or more of its node-runs is in a non-terminal pause state — `parked` (executor entered the park terminal waiting for a time-based or callback-based wake) or `held` (executor returned a held-claim terminal awaiting auto-terminal commit/abandon per `concept:node-run`). Held frames are surfaced via a held-frames diagnostics endpoint on the control API. They are normal during agent-driven work that includes external decisions and during held-claim work that spans multi-node holding subgraphs; persistently held frames may indicate stuck reviews and warrant investigation. Held-claim auto-terminal fires once every node in the holding subgraph completes, so held-claim release happens at the end of the holding subgraph, not at the per-node held-terminal boundary. A held frame is precisely a running frame with a node_run in `held` or `parked` (or `pending` / `stale` waiting indefinitely on a slow upstream); because every in-flight state holds the frame open, the held-frames diagnostic and the frame-end rule agree.

## Boundaries

Owns: the per-instance concurrency rule (≤1 running frame), the serial-per-instance ordering, the last-progress timestamp, frame-timeout warning emission, the triggering-message-id pointer that every frame carries, the root-RunScope pointer (created at frame start). Does NOT own: node state (lives on the node-run, see `concept:node-run`), claim conflict (lives in `concept:claim-handle`), scheduling cadence (lives in `concept:sensor`), the message itself (see `concept:message`), the message queue and its coalesce mode (lives on the instance, see `concept:instance`), RunScope internals (see `concept:run-scope`). Adjacent: `concept:cascade`, `concept:node`, `concept:node-run`, `concept:message`, `concept:instance`, `concept:run-scope`, `concept:sensor`.

## Invariants

- At most one `running` frame per instance.
- Frames have no pre-run state: every frame row is inserted directly into `running` at the pickup moment. There is no `queued` state; work waiting for a busy instance sits on the instance's message queue instead.
- Every frame row carries a non-null triggering-message reference. There is no path that creates a frame without a triggering message.
- Every frame row carries a non-null root-RunScope reference. The root RunScope is created at frame start in the same tx as the frame row insert (per `concept:run-scope`).
- Every dispatched run row carries a non-null frame reference, and lives in a RunScope inside that frame's RunScope tree.
- **Perfect frame isolation.** No node-run, RunScope, attribute row, wait-set row, or cascade-walker step ever crosses a frame boundary. Nothing in an already-running frame is observable to or mutable by a different frame.
- **Messages are the ONLY cross-frame carrier.** Message envelopes on the instance's message queue are the sole channel through which any data ever crosses a frame boundary. Every other channel — persisted attribute rows, run rows, node identity rows, signal audit rows — is invisible to any frame's runtime decisions. A frame receives one triggering message; the payload of that message is the only prior-frame-originating data the frame's work may observe.
- **No signal-emission decision may reach across frames.** The `attribute/<key>/changed` diff-gate baseline, `cascade_mode` bag-equality dedup, subscription-edge match, CEL `when:` evaluation, and every other cascade / gate / signal decision consult only data produced inside the running frame. The persisted attribute row of a prior frame's run is on disk for audit and operator observability, not for gate evaluation.
- **No frame processing may mutate persistent identity rows.** The `rimsky_nodes` table row is per-instance identity, set at instance creation, immutable during frame processing (per `concept:node`). Frame processing may mutate only per-run rows (`rimsky_node_runs`, `rimsky_node_attributes`) that were themselves created inside the same frame, plus the instance's message queue via the two channels enumerated in `concept:instance`.
- Frames are processed in arrival order per instance; cross-instance ordering is independent.
- The frame timeout is purely advisory: when the last-progress timestamp falls outside the window, the scheduler emits a single stuck-frame warning event and takes no destructive action.

## Common pitfalls

- **Rimsky's frame is not a stack frame, video frame, or UI frame.** A Rimsky frame is the unit of cascade resolution for an instance; nothing to do with call stacks, animation, or screen rendering.
- Treating frame ID as a sequence number with strong ordering. It's a UUID; ordering across frames is captured by the timestamps of frame-start events, not by ID arithmetic.
- Assuming frames span instances. A frame is per-instance; two instances of the same template have entirely separate frame populations.
- **Treating persisted attribute or run rows as legitimate cross-frame carriers.** Persisted state on disk exists for external observability (operator surfaces, audit tooling, forensics), not for the runtime to read back into a subsequent frame's decisions. A diff-gate, gate evaluator, or cascade-mode rule that consults a prior-frame row is a frame-isolation violation regardless of how "convenient" the lookup seems.
- **Confusing "cross-frame coupling" (of workflow shape) with "cross-frame state coupling" (of persisted data reads).** Multi-frame workflows are legitimate — a node emits a message, the next frame opens on that envelope, the receiver reads the message body. What travels is the message body. What does not travel is any observation of prior-frame node-run state.
- **Trying to make a workflow "self-terminate on value convergence" by observing prior-frame state.** Under frame isolation this is impossible: the current frame has no view of prior frames. A workflow that converges must carry its convergence signal in the message payload (rimsky-side) or observe convergence via external state (a claim producer's data, an HTTP node's response, etc.). Intra-frame CEL predicates on the settling verdict's payload can bound iteration inside one frame.
