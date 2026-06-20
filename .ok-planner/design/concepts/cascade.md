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

The cascade walker consults two edge maps — the subscription-edge map and the upstream-refresh edge map. Both feed the wait-set with the same row shape. Subscription edges are keyed by sender node-type (downstream lookup from a transitioning sender); upstream-refresh edges are keyed by receiver node-type (upstream lookup from a freshly-invalidated receiver), so the walker can proactively invalidate upstreams a receiver names with an upstream-refresh subscription. Under the subscription-edge map's empty sender-key sentinel, two kinds of edges coexist — cross-cutting subscriptions (fire on every settled sender, when the subscriber's predicate matches) and runtime-injected structural-root edges (fire only when the actual sender is the implicit empty-message virtual). The walker disambiguates the two kinds and they otherwise share the same code path.

## Invariants

- A cascade walk inserts a wait-set row for the receiver iff a subscription edge matches the emitted signal's type AND the subscriber's filter predicate evaluates true. The receiver is additionally stale-marked iff the matching subscription opts into wake-on-change.
- Cascade always happens in a frame.
- The cascade walker operates entirely within a single frame. It never creates a new frame; cross-frame coupling is expressed by message-emitter nodes whose dispatch lands a message in the ledger, with the next frame opening on the standard delivery path.
- Settled-color is informational. The functional equivalent of suppressing downstream auto-fire on a failed sender is expressed receiver-side via subscribers' `when:` predicates or via not subscribing to `terminal/error/*` at all.
- Staleness propagation — whether by invalidation walk or by sender settlement — does not by itself confer dispatch eligibility. Eligibility is the dispatch-time predicate per `concept:wait-set`, and the all-in-flight-upstreams-resolve-first guarantee is propagation-path-independent: a stale receiver does not dispatch while any subscribed upstream has an in-flight run in the same frame, no matter which path made it stale.
- A node-run's view of its upstreams is fixed for its lifetime. The substituted attribute bag the executor receives at first dispatch is the bag it receives on every subsequent invocation of the same node-run (the time-wake resume from `parked` reuses it; see `concept:parked-state`). Upstream nodes may re-run in the same frame, but those re-runs do not rewrite an in-flight downstream's inputs; the downstream sees the upstream's freshened value only at the dispatch of a NEW node-run (created by the normal cascade after the prior one settles).

## Common pitfalls

- **Rimsky's cascade is not CSS cascade.** CSS's cascade resolves competing style rules by specificity and order; Rimsky's cascade propagates staleness through the per-template subscription-edge inverse map. The two share a name and nothing else.
- Treating "recalculate" as a second message. Staleness propagation is a graph-traversal step, not a service message that travels alongside; recalculation is what the scheduler does next.
- Expecting cascade to skip nodes whose new value would be byte-identical to the old. Cascade is subscription-driven, not value-diff-driven; the executor commits a "no change" indicator on its emitted payload if it wants downstream subscribers that filter on that indicator to suppress.
- Confusing cascade reach with executor invocation. Cascade marks nodes stale and inserts wait-set rows; dispatch eligibility is decided at dispatch time by the two-condition predicate per `concept:wait-set` — no undrained wait-set rows for the run in the current frame AND no subscribed upstream with an in-flight run in the same frame — with claims and locks still to be acquired.
- Treating error-class subscribers as automatically downstream-firing. Under the subscriber-driven cascade model, an error-class subscriber fires only if it has declared the subscription; the sender's color does not fire downstream nodes by itself. A node that wants to halt propagation on errors simply omits the subscription; a node that wants to act on every error subscribes broadly.
