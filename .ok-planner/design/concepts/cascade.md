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
| **propagation** | Cascade-of-stale on `fresh_changed`. Mark dependents stale and recurse — the handler for `concept:invalidate`. |
| **fallthrough** | No-dispatch fresh-roll on `pure_cascade`. Roll fresh state forward without running the node; detected per-node and executed by the scheduler's pure-cascade sweep. |

One walk; two node-level behaviors (propagation, fallthrough).

## Purpose

A reactive graph orchestrator only earns its keep if a single executor's "I changed" signal causes the right downstream nodes to recompute and no others. Cascade is the mechanism that turns one terminal outcome into the set of downstream node-state transitions.

## Boundaries

Owns: the firing-gate predicate, the downstream walk, the two node-level behaviors (propagation vs fallthrough). Does NOT own: invalidate emission (see `concept:invalidate`), frame scheduling (see `concept:frame`), terminal-handler resolution (see `concept:terminal-resolution`). Adjacent: `concept:invalidate`, `concept:signal`, `concept:transition-reason`, `concept:frame`, `concept:terminal-resolution`.

The cascade walker consults two edge maps — the subscription-edge map and the hard-dep edge map. Both feed the wait-set with the same row shape. Subscription edges are keyed by sender node-type (downstream lookup from a transitioning sender); hard-dep edges are keyed by receiver node-type (upstream lookup from a freshly-invalidated receiver), so the walker can proactively invalidate upstreams that a receiver declares `hard_dep: true` on.

## Invariants

- Cascade fires iff a subscription edge matches the emitted signal's type AND the subscriber's CEL `when:` predicate evaluates true.
- Cascade always happens in a frame.
- The walk + per-node behaviors are scheduler actions; they are NOT configurable via the per-emit `frame: in | next` discipline.
- Settled-color is informational. The functional equivalent of suppressing downstream auto-fire on a failed sender is expressed receiver-side via subscribers' `when:` predicates or via not subscribing to `terminal/error/*` at all.

## Common pitfalls

- **Rimsky's cascade is not CSS cascade.** CSS's cascade resolves competing style rules by specificity and order; Rimsky's cascade propagates `invalidate` through the per-template subscription-edge inverse map. The two share a name and nothing else.
- Treating "recalculate" as a second message. There is one cascade message: `invalidate`. Recalculation is what the scheduler does next, not a service message that travels alongside.
- Expecting cascade to skip nodes whose new value would be byte-identical to the old. Cascade is subscription-driven, not value-diff-driven; the executor commits `changed: false` if it wants downstream subscribers that filter on `payload.changed` to suppress.
- Confusing cascade reach with executor invocation. Cascade marks nodes stale and inserts wait-set rows; the scheduler decides which stale nodes are eligible for dispatch (wait-set empty for the current frame, claims and locks acquirable).
- Treating `terminal/error/*` subscribers as automatically downstream-firing. Under the subscriber-driven cascade model, a subscriber filtering on `terminal/error/*` fires only if it has declared the subscription; the sender's color does not fire downstream nodes by itself. A node that wants to halt propagation on errors simply omits the subscription; a node that wants to act on every error subscribes broadly.
