---
concept: cascade
status: as-is
aliases:
  - reactive-cascade
---

# Cascade

## What it is

Cascade is the engine that turns one node-state transition into the set of downstream node-state transitions. Three precise words name its parts:

| Word | Meaning |
|---|---|
| **walk** | The scheduler-tick-driven traversal of the graph (topology-ordered). The mechanism. |
| **propagation** | Cascade-of-stale on `fresh_changed`. Mark dependents stale and recurse. |
| **fallthrough** | No-dispatch fresh-roll on `pure_cascade`. Roll fresh state forward without running the node; detected per-node and executed by the scheduler's pure-cascade sweep. |

One walk; two node-level behaviors (propagation, fallthrough).

## Purpose

A reactive graph orchestrator only earns its keep if a single executor's "I changed" signal causes the right downstream nodes to recompute and no others. Cascade is the mechanism that turns one terminal outcome into the set of downstream node-state transitions.

## Boundaries

Owns: the firing-gate predicate, the downstream walk, the two node-level behaviors (propagation vs fallthrough). Does NOT own: invalidate emission (see `concept:message`), frame creation (see `concept:frame`), terminal-handler resolution (see `concept:terminal-resolution`). Adjacent: `concept:message`, `concept:signal`, `concept:transition-reason`, `concept:frame`, `concept:terminal-resolution`.

The cascade walker consults two edge maps — the subscription-edge map and the upstream-refresh edge map. Both feed the wait-set with the same row shape. Subscription edges are keyed by sender node-type (downstream lookup from a transitioning sender); upstream-refresh edges are keyed by receiver node-type (upstream lookup from a freshly-invalidated receiver), so the walker can proactively invalidate upstreams a receiver names with an upstream-refresh subscription. Under the subscription-edge map's empty sender-key, runtime-injected structural-root edges live — consulted when the implicit empty-message receiver settles, waking every structural root of the template.

## Invariants

- A cascade walk inserts a wait-set row for the receiver iff a subscription edge matches the emitted signal's type AND the subscriber's filter predicate evaluates true. The receiver is additionally stale-marked iff the matching subscription opts into wake-on-change.
- Cascade always happens in a frame.
- The cascade walker operates entirely within a single frame. It never creates a new frame; cross-frame coupling is expressed by message-sender nodes whose dispatch lands a message in the ledger, with the next frame opening on the standard delivery path.
- Settled-color is informational. The functional equivalent of suppressing downstream auto-fire on a failed sender is expressed receiver-side via subscribers' `when:` predicates or via not subscribing to `terminal/error/*` at all.
- **In-flight node-runs are sealed against cascade-driven mutation.** A node-run in any in-flight state (`pending`, `stale`, `running`, `held`, `parked` per `concept:node-run`) is never re-invalidated, never has its state mutated by the cascade walker, never has its persisted attribute bag rewritten by anything other than its own executor's writeback. When a cascade walk targets a receiver that already has an in-flight run, the walker creates a NEW cascade-driven pending row (or accumulates the wait-set entry into the latest pending per the per-sender-node accumulation rule below); the existing in-flight run is left untouched. The downstream sees the upstream's freshened value only at the dispatch of the new node-run, which the dispatcher claims after the existing in-flight run settles (the serialization gate refuses to claim while the same (node, run-scope) has a run in {running, held, parked}).
- **Per-sender-node accumulation rule** (the walker's accumulate-or-queue gate): on each cascade walk, find the receiver's latest cascade-driven pending in the current frame. If none exists, create a new pending. If one exists AND the sender's node is NOT in that pending's wait-set sender-nodes, accumulate (insert the wait-set row into the existing pending). If one exists AND the sender's node IS already in that pending's wait-set sender-nodes, create a NEW pending (the previous pending is sealed; subsequent cascades from other sender-nodes accumulate into the new one). Multiple cascade-driven pendings can coexist per (receiver, run-scope, frame); the latest is always the accumulation target. See `decision:walker-rule-per-sender-node` for the rationale.
- **Cascade-defer for held**: when a node-run's terminal includes a held=true claim, the cascade walker does NOT fire at the held terminal. The walk is deferred until the auto-terminal handler resolves: Commit walks with `terminal/success`; Abandon walks with `terminal/error/abandoned`. See `decision:held-as-state-not-phase` and `decision:terminal-error-abandoned-as-error-class`.

## Common pitfalls

- **Rimsky's cascade is not CSS cascade.** CSS's cascade resolves competing style rules by specificity and order; Rimsky's cascade propagates staleness through the per-template subscription-edge inverse map. The two share a name and nothing else.
- Treating "recalculate" as a second message. Staleness propagation is a graph-traversal step, not a service message that travels alongside; recalculation is what the scheduler does next.
- Expecting cascade to skip nodes whose new value would be byte-identical to the old. Cascade is subscription-driven, not value-diff-driven; the executor commits a "no change" indicator on its emitted payload if it wants downstream subscribers that filter on that indicator to suppress.
- Confusing cascade reach with executor invocation. Cascade creates new pending rows and inserts wait-set rows; the gate evaluator transitions pending→stale when the wait-set drains and no subscribed upstream has an in-flight run; the dispatcher claims a stale row when the serialization gate (no same-(node, run-scope) run in running/held/parked) clears. Cascade does NOT directly invoke executors; it queues work.
- Treating error-class subscribers as automatically downstream-firing. Under the subscriber-driven cascade model, an error-class subscriber fires only if it has declared the subscription; the sender's color does not fire downstream nodes by itself. A node that wants to halt propagation on errors simply omits the subscription; a node that wants to act on every error subscribes broadly.
