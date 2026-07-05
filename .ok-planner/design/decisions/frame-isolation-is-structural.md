---
decision: frame-isolation-is-structural
status: as-is
aliases: []
---

# Frame isolation is a structural invariant, not a tunable

## Choice

Frame isolation is a structural, load-bearing invariant of rimsky: no state ever crosses a frame boundary through any channel other than a message envelope on the instance's queue. This applies uniformly to every runtime surface — substitution reads, gate-evaluator baselines, cascade-mode dedup, `attribute/<key>/changed` diff-gate, wait-set inserts, cascade-walker steps, operator-triggered `recalculate` / `wake_parked`, and every future mechanism. There is no "widened" mode, no per-node opt-in flag, no per-signal exception, no fallback path when the same-scope lookup returns nothing. The persistence layer's queries return nothing across a frame boundary because RunScopes never span frames (per `concept:run-scope`), and the runtime never reaches for a cross-scope fallback.

The invariant lives at the boundaries of six concepts. Each owns its piece and states it positively:

- `concept:frame` — the isolation invariant itself; the list of legitimate cross-frame channels (messages only); the prohibition on signal-emission decisions reaching across frames.
- `concept:node` — the `rimsky_nodes` row is per-instance identity, immutable during frame processing.
- `concept:instance` — the two legitimate frame-processing mutations on the instance row (message queue append + coalesce cancellation); every other field is control-API only.
- `concept:attribute` — every new frame's root RunScope starts fresh; the runtime never hydrates a new frame's run from a prior frame's row.
- `concept:signal` — the `attribute/<key>/changed` diff-gate baseline is same-RunScope only; no cross-scope fallback; no signal-emission decision reads persisted state from a prior frame.
- `concept:cascade-mode` — every mode's `(receiver, run-scope)` lookup is intra-frame by construction.

## Rationale

Frame isolation is what makes cascade resolution tractable, auditable, and composable. Every frame is a self-contained unit of work whose reasoning fits inside one boundary; the only thing that carries across boundaries is the message payload — a durable, operator-observable, replayable envelope on the instance's queue. That property is what lets:

- The cascade walker terminate: an intra-frame cycle either converges declaratively (via CEL predicates, `cascade_mode`, or diff-gated attribute rounds inside the frame) or it doesn't, but it never reaches into another frame's state to help decide.
- The audit story stay coherent: every frame's work attributes back to its triggering message; every signal it emitted lives in one frame's ledger; every attribute row it wrote is per-run inside one frame.
- The operator model stay simple: an instance's message queue holds all inter-frame coupling, visible and cancellable. Nothing else needs to travel.
- The persistence model stay narrow: per-run rows are per-frame; per-node identity rows are per-instance; the query surface never has to answer "give me a prior frame's state" because the runtime never asks.

Any weakening — even one carefully-scoped "cross-frame fallback for root-scope runs to enable convergence" — is load-bearing failure. Once the runtime can observe a prior frame's persisted state to make a current-frame decision, the isolation invariant is gone: two frames are now coupled through persisted state, the operator-model claim (messages are the only cross-frame carrier) is false, and the design catalog can no longer be trusted to describe the system. The `queue-drain-converges` story (drafted mid-2026-07 and retired within the same session) is the case study: an author invited to widen the diff-gate for cross-frame convergence produced code, tests, and design prose that made the illegitimate mechanism look normal. The correction is not "narrow the fallback back down" but "there is no fallback, ever, by construction." The persistence layer stops offering it; the concept catalog stops describing it; the story catalog stops promising it.

Multi-frame workflows are legitimate and expected — a node emits a message via `concept:message-emitter-node`, the envelope lands on the instance's queue, the next frame opens on that envelope, the receiver reads the message body. What travels is the message body. What does not travel is any observation of prior-frame node-run state. Workflows that need to converge on a stable value across frames encode that convergence in the message payload (e.g. the emit-node's CEL `when:` predicate over the settling verdict's `payload.attributes_delta` gates emission — when the workflow is done, no message is emitted, the queue drains, no more frames open) or observe it via external state (a claim producer's data, an HTTP node's response). The queue emptying **is** convergence at the workflow level; the platform does not need — and does not have — a cross-frame observation mechanism to implement it.

## Alternatives

- **Widen the `attribute/<key>/changed` diff-gate to compare against the most-recent cross-frame prior for root-scope runs.** Rejected. Introduced in commit `20724e96` (2026-07-05), retired in the frame-isolation restoration sweep. The widening was framed as "the diff-gate's meaning is global for a given node's history" — that framing is wrong: the diff-gate's meaning is bounded by the frame, because signal emission is bounded by the frame, because every runtime decision is bounded by the frame. The rationale for retiring is the failure-mode described in this decision's Rationale.
- **Per-node opt-in flag for cross-frame carry-forward or cross-frame diff-gate.** Rejected. Any opt-in surface makes frame isolation a policy rather than a structural invariant, and every author who reaches for the flag will produce cross-frame-coupled workflows that break the operator model. The right primitives for stateful cross-frame workflows already exist: message payloads, `params`, claim payloads, external state via claim producers.
- **Frame-count ceilings or similar operator-visible "runaway prevention".** Rejected. The queue-empty condition already terminates multi-frame workflows naturally; a ceiling would mask author-side bugs (an emit-node that fires unconditionally when it should have been CEL-gated) and hide behavior the operator should see.
