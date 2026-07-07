---
decision: frame-isolation-is-structural
status: as-is
aliases:
  - frame-isolation
---

# Frame isolation is a structural invariant, not a tunable

## Choice

A frame is one complete run of the graph, with no side effects from earlier runs and none against future runs. That is what the concept exists to mean: no node-run, RunScope, attribute bag, or wait-set row ever crosses a frame boundary, and the only channel through which any data crosses is a message envelope on the instance's queue. A message lands only in the frame it triggered. Cross-frame coupling, when a workflow needs it, is expressed by a message-sender node whose dispatch lands a message in the ledger, with the next frame opening on the standard delivery path per `decision:single-frame-creation-path`.

The invariant is structural, not policy. It applies uniformly to every runtime surface — substitution reads, gate-evaluator baselines, cascade-mode dedup, the `attribute/<key>/changed` diff-gate, wait-set inserts, cascade-walker steps, operator-triggered `recalculate` / `wake_parked`, and every future mechanism. There is no "widened" mode, no per-node opt-in flag, no per-signal exception, no fallback path when a same-scope lookup returns nothing. The persistence layer's queries return nothing across a frame boundary because RunScopes never span frames (per `concept:run-scope` and `decision:run-scope-is-per-frame`), and the runtime never reaches for a cross-scope fallback.

The invariant lives at the boundaries of six concepts. Each owns its piece and states it positively:

- `concept:frame` — the isolation invariant itself; the list of legitimate cross-frame channels (messages only); the prohibition on signal-emission decisions reaching across frames.
- `concept:node` — the `rimsky_nodes` row is per-instance identity, immutable during frame processing.
- `concept:instance` — the two legitimate frame-processing mutations on the instance row (message queue append + coalesce cancellation); every other field is control-API only.
- `concept:attribute` — every new frame's root RunScope starts fresh; the runtime never hydrates a new frame's run from a prior frame's row.
- `concept:signal` — the `attribute/<key>/changed` diff-gate baseline is same-RunScope only; no cross-scope fallback; no signal-emission decision reads persisted state from a prior frame.
- `concept:cascade-mode` — every mode's `(receiver, run-scope)` lookup is intra-frame by construction.

## Rationale

Frame isolation is what makes cascade resolution deterministic, tractable, auditable, and composable. A frame's behavior is fully determined by its triggering message and the frame's own work; whatever a prior frame committed lives in the database as that frame's terminal effect, never as transient state the next frame inherits. The only thing that carries across boundaries is the message payload — a durable, operator-observable, replayable envelope on the instance's queue. That property is what lets:

- The runtime stay deterministic: the same graph and the same message produce the same frame, because nothing a concurrent or earlier frame did can reach into the current frame's reads.
- The cascade walker terminate: an intra-frame cycle either converges declaratively (via CEL predicates, `cascade_mode`, or diff-gated attribute rounds inside the frame) or it doesn't, but it never reaches into another frame's state to help decide.
- The audit story stay coherent: every frame's work attributes back to its triggering message; every signal it emitted lives in one frame's ledger; every attribute row it wrote is per-run inside one frame.
- The operator model stay simple: an instance's message queue holds all inter-frame coupling, visible and cancellable. Nothing else needs to travel.
- The persistence model stay narrow: per-run rows are per-frame; per-node identity rows are per-instance; the query surface never has to answer "give me a prior frame's state" because the runtime never asks. Isolation is implicit in RunScope identity, so no query needs a per-call frame qualifier — and no query can forget one.

Any weakening — even one carefully-scoped "cross-frame fallback to enable convergence" — is load-bearing failure. Once the runtime can observe a prior frame's persisted state to make a current-frame decision, the isolation invariant is gone: two frames are coupled through persisted state, the operator-model claim (messages are the only cross-frame carrier) is false, and the design catalog can no longer be trusted to describe the system.

Multi-frame workflows are legitimate and expected — a node sends a message via `concept:message-sender-node`, the envelope lands on the instance's queue, the next frame opens on that envelope, the receiver reads the message body. What travels is the message body. What does not travel is any observation of prior-frame node-run state. Workflows that need to converge on a stable value across frames encode that convergence in the message payload (e.g. the send-node's CEL `when:` predicate over the settling verdict's `payload.attributes_delta` gates emission — when the workflow is done, no message is sent, the queue drains, no more frames open) or observe it via external state (a claim producer's data, an HTTP node's response). The queue emptying **is** convergence at the workflow level; the platform does not need — and does not have — a cross-frame observation mechanism to implement it.

## Alternatives

- **Shared state across frames (long-lived RunScopes; carry-forward, sequence numbering, and the wait-set tail surviving frame boundaries).** Rejected. Three consequences rule it out: (1) every persistence query for "state in this scope" must be re-qualified with a frame filter to avoid cross-frame leakage, multiplying the surface area for missing-filter bugs; (2) the cascade walker's in-flight-sealed invariant (per `concept:cascade`) becomes violable when one frame's mid-flight run is visible to another frame's gate evaluator; (3) "the latest run for this node in this scope" becomes ambiguous across frames and requires clock-based tiebreakers.
- **Widen the `attribute/<key>/changed` diff-gate to compare against the most-recent cross-frame prior for root-scope runs.** Rejected. The framing "the diff-gate's meaning is global for a given node's history" is wrong: the diff-gate's meaning is bounded by the frame, because signal emission is bounded by the frame, because every runtime decision is bounded by the frame. Cross-frame convergence belongs in message payloads or external state, not in signal-emission decisions.
- **Per-node opt-in flag for cross-frame carry-forward or cross-frame diff-gate.** Rejected. Any opt-in surface makes frame isolation a policy rather than a structural invariant, and every author who reaches for the flag will produce cross-frame-coupled workflows that break the operator model. The right primitives for stateful cross-frame workflows already exist: message payloads, `params`, claim payloads, external state via claim producers.
- **Isolation by convention (no structural property, just discipline at each call site).** Rejected. Discipline across many code paths and many author generations does not hold; a convention is violated the first time a lookup with a cross-frame result looks convenient. Making isolation structural — the queries cannot return cross-frame state — is the only durable form.
- **Frame-count ceilings or similar operator-visible "runaway prevention".** Rejected. The queue-empty condition already terminates multi-frame workflows naturally; a ceiling would mask author-side bugs (a send-node that fires unconditionally when it should have been CEL-gated) and hide behavior the operator should see.
