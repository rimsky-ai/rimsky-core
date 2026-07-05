---
story: queue-drain-converges
status: as-is
---

# Template author expresses multi-frame queue-drain workflows that terminate on convergence

## Role

As a template author, I can express a workflow that unfolds across a series of frames — each frame's outcome producing the message that triggers the next frame on the same instance — so that a long-running iteration composes with rimsky's frame semantics (one message, one frame; one frame at a time per instance). The workflow terminates declaratively when its driving value converges — not via an operator-supplied frame-count ceiling and not via runtime-private state.

## Capability

A node in the template carries `emits_message: <type>` (see `story:cascade-emit`). When its subscriptions fire during a frame, the emit-node dispatches: substitution resolves its attributes from upstreams, the runtime constructs a message envelope with the resolved attribute set as the body, and inserts it into the instance's message queue. The next frame opens carrying that envelope as its trigger (per `concept:frame`). Loops are bounded declaratively: a CEL `when:` predicate on the emit-node's subscription can suppress emit when the upstream signals convergence; or the diff-gate on `attribute/<key>/changed` (per `concept:signal`) can suppress the emit-node's wake when the driving attribute's value across frames stops changing.

## Business value

A workflow can span many frames without the operator having to bound it with a frame-count ceiling or thread runtime-private counters through executor state. Convergence lives in the template's subscriptions and gates — either as a CEL predicate over the settling verdict's payload or as the value-convergence semantics of the diff-gate. The instance's queue is the substrate; the message envelope is the cross-frame carrier; the diff-gate or CEL predicate is the stop condition.

## Acceptance

A worker + emit-node pair converges via the diff-gate. Template declares a worker node and an emit-node subscribed to `attribute/step/changed` on the worker. A wake message arrives; frame 1 opens; worker settles with step=1; `attribute/step/changed` fires (no prior value); emit-node dispatches; message envelope lands on the instance's queue; frame 2 opens with that envelope as its trigger; worker settles with step=1 again; the diff-gate compares against the worker's prior settlement across the frame boundary and does not fire (same value); the emit-node does not wake; the loop ends. Total: worker runs twice, emit-node runs once, one message envelope in the ledger, two frames.

A back-edge variant terminates via a CEL `when:` gate. Template declares nodes A, B, and an emit-node whose subscription to B carries `when: payload.attributes_delta.should_loop`. A wake message arrives; frame 1 opens; A settles; B settles with `should_loop: false`; the emit-node's CEL predicate evaluates false; no message envelope emits; the loop ends. Bounded by the CEL predicate against B's settling payload.

## Falsifier

The emit-node continues to dispatch across successive frames after the driving attribute value has converged (diff-gate leaking cross-frame, failing to suppress the same-value emission); OR the CEL `when:` gate on the emit-node's subscription evaluates false but a message envelope emits anyway; OR frame N opens without the message envelope from frame N-1's emit-node in the ledger.

## Proof

An executable self-drain scenario: worker + emit-node cross-frame loop bounded by the widened diff-gate. Assertions: worker runs exactly twice, emit-node runs exactly once, one message envelope in the ledger, loop terminates without operator intervention. Plus an executable back-edge scenario proving CEL-predicated termination: an A → B → emit-node cycle where the emit-node's `when:` predicate over B's `terminal/success` payload evaluates false, no envelope emits, the recurrence-driving path stops.
