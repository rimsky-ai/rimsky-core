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
| **propagation** | Cascade-of-stale on a subscription-edge match (sender node-type × emitted settling signal type) whose `when:` predicate evaluates true. Mark dependents stale and recurse. |
| **fallthrough** | No-dispatch fresh-roll for a node with no executor. Roll fresh state forward without running the node; detected per-node (executor unset) and executed by the scheduler's pure-cascade sweep, which records the transition-reason `pure_cascade`. |

One walk; two node-level behaviors (propagation, fallthrough).

## Purpose

A reactive graph orchestrator only earns its keep if a single executor's "I changed" signal causes the right downstream nodes to recompute and no others. Cascade is the mechanism that turns one terminal outcome into the set of downstream node-state transitions.

## Boundaries

Owns: the firing-gate predicate, the downstream walk, the two node-level behaviors (propagation vs fallthrough). Does NOT own: invalidate emission (see `concept:message`), frame creation (see `concept:frame`), terminal-handler resolution (see `concept:terminal-resolution`), the queue-shaping rules applied at gate-clear (see `concept:cascade-mode`). Adjacent: `concept:message`, `concept:signal`, `concept:transition-reason`, `concept:frame`, `concept:terminal-resolution`, `concept:cascade-mode`.

The cascade walker consults two edge maps — the subscription-edge map and the upstream-refresh edge map. Both feed the wait-set with the same row shape. Subscription edges are keyed by sender node-type (downstream lookup from a transitioning sender); upstream-refresh edges are keyed by receiver node-type (upstream lookup from a freshly-invalidated receiver), so the walker can proactively invalidate upstreams a receiver names with an upstream-refresh subscription. Under the subscription-edge map's empty sender-key, runtime-injected structural-root edges live — consulted when the implicit empty-message receiver settles, waking every structural root of the template.

## Invariants

- **Cascade fires only on settling signals.** The firing-gate predicate admits exactly the settling signal kinds — `terminal/success`, `terminal/error/<class>`, and `attribute/<key>/changed` — and rejects every other signal kind outright, before any subscription-edge lookup runs. A single predicate is the sole authority for both what a template may subscribe to and what actually reaches the walk at runtime, so the subscribable set is derived from that predicate rather than tracked as a second, independently-maintained list. Dispatch-internal signals never cascade and are not subscribable; template registration rejects any subscription that targets one.
- A cascade walk inserts a wait-set row for the receiver whenever a subscription edge matches the emitted signal's type AND the subscriber's `when:` predicate evaluates true; the receiver is unconditionally stale-marked on that match — every declared subscription is a wake declaration, there is no separate wake opt-in. An upstream-refresh edge additionally inserts a wait-set row for a receiver's own named upstream independent of any emitted signal or filter, proactively invalidating that upstream within the same walk.
- Cascade always happens in a frame.
- The cascade walker operates entirely within a single frame. It never creates a new frame; cross-frame coupling is expressed by message-sender nodes whose dispatch lands a message in the ledger, with the next frame opening on the standard delivery path.
- Settled-color — the fresh/failed label a node-run's terminal outcome carries — is informational. The functional equivalent of suppressing downstream auto-fire on a failed sender is expressed receiver-side via subscribers' `when:` predicates or via not subscribing to `terminal/error/*` at all.
- **In-flight node-runs are sealed against cascade-driven mutation, with one narrow parked-wake carve-out.** A node-run in any in-flight state (`pending`, `stale`, `running`, `held`, `parked` per `concept:node-run`) is never re-invalidated and never has its persisted attribute bag rewritten by anything other than its own executor's writeback. When a cascade walk targets a receiver that already has an in-flight run, the walker creates a NEW cascade-driven pending row (or accumulates the wait-set entry into the latest pending per the per-sender-node accumulation rule below); the existing in-flight run's bag and identity are left untouched. The one state mutation the walker performs on an existing run: a PARKED receiver run is woken in the walk's transaction — parked → stale through the single parked-wake path, resume-at cleared, wake event appended — so the interrupted work resumes promptly instead of sleeping until its resume-at while fresh upstream information waits behind it (see `concept:parked-state`). The woken run re-dispatches with its own preserved bag and scratch; the cascade round itself still lands on the new pending row's wait-set, and the downstream sees the upstream's freshened value only at the dispatch of that new node-run, which the dispatcher claims after the woken run settles (the serialization gate refuses to claim while the same (node, run-scope) has a run in {running, held, parked}).
- **Per-sender-node accumulation rule** (the walker's accumulate-or-queue gate): on each cascade walk, find the receiver's latest cascade-driven pending in the current frame. If none exists, create a new pending. If the triggering sender node-run already has a wait-set row on that pending, accumulate — a second matching signal from the same node-run (for example a terminal signal followed by an attribute-changed signal from the same settle) never opens a new pending. Otherwise, if the sender's node is NOT in that pending's wait-set sender-nodes, accumulate (insert the wait-set row into the existing pending). If the sender's node IS already in that pending's wait-set sender-nodes from a different node-run, create a NEW pending (the previous pending is sealed; subsequent cascades from other sender-nodes accumulate into the new one). Multiple cascade-driven pendings can coexist per (receiver, run-scope, frame); the latest is always the accumulation target. See `decision:walker-rule-per-sender-node` for the rationale.
- **Cascade-defer for held**: when a node-run's terminal includes a held=true claim, the cascade walker fires immediately at the held terminal but filtered to subgraph co-members only — held-subgraph members keep cascading among themselves during the hold. Non-member receivers are deferred until the auto-terminal handler resolves the holder's full claim portfolio: Commit walks with `terminal/success`; Abandon walks with `terminal/error/abandoned`. See `decision:held-as-state-not-phase` and `decision:terminal-error-abandoned-as-error-class`.

## Common pitfalls

- **Rimsky's cascade is not CSS cascade.** CSS's cascade resolves competing style rules by specificity and order; Rimsky's cascade propagates staleness through the per-template subscription-edge inverse map. The two share a name and nothing else.
- Treating "recalculate" as a second message. Staleness propagation is a graph-traversal step, not a service message that travels alongside; recalculation is what the scheduler does next.
- Expecting cascade to skip nodes whose new value would be byte-identical to the old. Cascade is subscription-driven, not value-diff-driven; the executor commits a "no change" indicator on its emitted payload if it wants downstream subscribers that filter on that indicator to suppress.
- Confusing cascade reach with executor invocation. Cascade creates new pending rows and inserts wait-set rows; the gate evaluator transitions pending→stale when the wait-set drains and no subscribed upstream has a genuinely blocking in-flight run — a held upstream that is a subgraph co-member of the receiver does not gate it. At that same gate-clear point the node's `concept:cascade-mode` rule can drop the pending outright instead of advancing it. The dispatcher claims a stale row when the serialization gate (no same-(node, run-scope) run in running/held/parked) clears. Cascade does NOT directly invoke executors; it queues work.
- Treating error-class subscribers as automatically downstream-firing. Under the subscriber-driven cascade model, an error-class subscriber fires only if it has declared the subscription; the sender's color does not fire downstream nodes by itself. A node that wants to halt propagation on errors simply omits the subscription; a node that wants to act on every error subscribes broadly.
