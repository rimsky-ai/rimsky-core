---
concept: cascade
status: as-is
aliases:
  - reactive-cascade
references:
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
  - _discover/2026-05-10-frame-resolution-model.md
  - _discover/2026-05-10-state-machine-no-self-loop.md
---

# Cascade

## What it is

Cascade is the engine that turns one node-state transition into the set of downstream node-state transitions. Per the 2026-05-12 nomenclature resolution, three precise words name its parts:

| Word | Meaning | Implementation site |
|---|---|---|
| **walk** | The scheduler-tick-driven traversal of the graph (topology-ordered). The mechanism. | `code:runtime/conductor.go::tick` |
| **propagation** | Cascade-of-stale on `fresh_changed`. Mark dependents stale and recurse. | `code:runtime/cascade_invalidate.go::InvalidateNode` (handler for `concept:invalidate`) |
| **fallthrough** | No-dispatch fresh-roll on `pure_cascade`. Roll fresh state forward without running the node. | Per-node detection in `code:runtime/cascade_recalculate.go::RecalculateNode`; executed by the scheduler's pure-cascade sweep. |

One walk; two node-level behaviors (propagation, fallthrough).

## Purpose

A reactive graph orchestrator only earns its keep if a single executor's "I changed" signal causes the right downstream nodes to recompute and no others. Cascade is the mechanism that turns one terminal outcome into the set of downstream node-state transitions.

## Boundaries

Owns: the firing-gate predicate, the downstream walk, the two node-level behaviors (propagation vs fallthrough). Does NOT own: invalidate emission (see `concept:invalidate`), frame scheduling (see `concept:frame`), terminal-handler resolution (see `concept:lifecycle-handler`). Adjacent: `concept:invalidate`, `concept:last-outcome`, `concept:transition-reason`, `concept:frame`, `concept:lifecycle-handler`.

The cascade walker consults two edge maps — the subscription-edge map (existing) and the hard-dep edge map (added 2026-05-20). Both feed the wait-set with the same row shape. Subscription edges are keyed by sender node-type (downstream lookup from a transitioning sender); hard-dep edges are keyed by receiver node-type (upstream lookup from a freshly-invalidated receiver), so the walker can proactively invalidate upstreams that a receiver declares `hard_dep: true` on.

## Invariants

- Cascade fires iff a subscription edge matches the emitted signal's type AND the subscriber's CEL `when:` predicate evaluates true.
- Cascade always happens in a frame.
- The walk + per-node behaviors are scheduler actions; they are NOT configurable via the per-emit `frame: in | next` discipline.

> **Retracted 2026-05-23.** Under the subscriber-driven cascade-fire model introduced by spec `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md`, propagation is determined by subscriber matches against the emitted signal, not by sender color. Settled-color is informational. The functional equivalent (downstream nodes not auto-firing on a failed sender) is now expressed receiver-side via subscribers' `when:` predicates or via not subscribing to `terminal/error/*` at all. The matching retraction lives on `concepts/parked-state.md`.

## Aliases and historical names

The phrase "reactive cascade" appears in sketches and human-facing onboarding docs. Internally, "cascade" is the unambiguous name. Pre-2026-05-12 prose sometimes referred to "two walks"; the current vocabulary is one walk + two node-level behaviors.

## Common pitfalls

- **Rimsky's cascade is not CSS cascade.** CSS's cascade resolves competing style rules by specificity and order; Rimsky's cascade propagates `invalidate` through the per-template subscription-edge inverse map. The two share a name and nothing else.
- Treating "recalculate" as a second message. There is one cascade message: `invalidate`. Recalculation is what the scheduler does next, not a service message that travels alongside.
- Expecting cascade to skip nodes whose new value would be byte-identical to the old. Cascade is subscription-driven, not value-diff-driven; the executor commits `changed: false` if it wants downstream subscribers that filter on `payload.changed` to suppress.
- Confusing cascade reach with executor invocation. Cascade marks nodes stale and inserts wait-set rows; the scheduler decides which stale nodes are eligible for dispatch (wait-set empty for the current frame, claims and locks acquirable).
- Treating `terminal/error/*` subscribers as automatically downstream-firing. Under the subscriber-driven cascade model, a subscriber filtering on `terminal/error/*` fires only if it has declared the subscription; the sender's color does not fire downstream nodes by itself. A node that wants to halt propagation on errors simply omits the subscription; a node that wants to act on every error subscribes broadly.

## Notes

- Three-word vocabulary (walk / propagation / fallthrough) introduced per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #10). Resolves `tension:cascade-walks-overloaded`.
- 2026-05-14: the cascade walk's downstream traversal is driven by the per-template subscription-edge inverse map (see `concept:node-subscription`), not by a static dependency graph. Wait-set rows are inserted on every cascade-walk match (pessimistic invalidate); the bulk-delete-on-settled-state rule (see `concept:wait-set`) drains them as senders resolve. Eligibility = state=stale AND wait-set is empty for the current frame (predicate evaluated in the persistence-layer SweepReady query at `code:foundation/persistence/postgres/nodes.go::ListReadyForDispatch`). Per spec `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. (Superseded 2026-05-22: wait-set insertion now uses the affirm-then-read pattern — `AffirmNodeRunRow` owns row allocation; the walker reads the affirmed row's id and inserts the wait-set row keyed by that id.)
- 2026-05-15: **sub-graph encapsulation**. Cascade walks descend through delegation (the calling node fires per its own subscriptions), but cascade does NOT cross the sub-graph boundary from outside. Outer subscriptions match against the calling node's state/events/attributes (populated from the parent run's lifecycle including the carried-up exit writeback per `concept:delegation`). Internal nodes within the sub-graph cascade normally among each other, with entry-alias references resolved to the calling node per-invocation. Cascade-boundary opacity is enforced at canonicalization: internal nodes referencing outer-graph nodes are rejected at template registration. See `concept:sub-graph`, `concept:delegation`.
- [2026-05-18] Folded content from former `docs/concepts/cascade.md` (now retired) — common-pitfalls subsection (CSS-cascade disambiguation + recalculate/value-diff/dispatch-vs-cascade-reach pitfalls).
- 2026-05-20 — Hard-dep edge map. The cascade walker now consults `BuildHardDepEdges` alongside `BuildSubscriptionEdges` at registration. At runtime, when invalidating a receiver R, the walker iterates R's hard-dep edges (computed from R's attribute schema fields with `hard_dep: true`); for each (R, X) hard-dep edge where X has no current-frame run, the walker proactively invalidates X via an inline stale-mark + recursive cascade walk in the same transaction, then inserts a wait-set blocker on R. See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`.
- 2026-05-22 — Reshape per spec `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`: cascade walker is RunScope-aware (per `concept:run-scope`). For each subscription edge the walker computes a target RunScope (same-RunScope is the common case; cross-RunScope at sub-graph entry-success and fan-out parent settlement), then calls `AffirmNodeRunRow(receiver_node_id, target_run_scope_id, frame_id)` to ensure the receiver's run row exists, reads its id via `GetInFlightRunForNode`, and inserts the wait-set row keyed by the resolved id. `MarkStaleForCascade` simplifies to a pure UPDATE keyed by `run_id` (no insert path; allocation is owned by `AffirmNodeRunRow`).
- 2026-05-23 — Reshape per spec `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md`. Cascade-fire predicate becomes subscriber match (`concept:signal`); the sender-side `last_outcome == fresh_changed` gate retires. Filter evaluation moves to walk-time (CEL predicates against signal payload); the pessimistic-invalidate rule (insert wait-set rows regardless of filter) retires in favor of subscriber-driven matching. The "cascade does not propagate from parked or failed" invariant retracts — propagation is subscriber-driven, not sender-color-driven (the matching retraction lives on `concepts/parked-state.md`). Walker now fires once per emitted signal (terminal/success + one attribute/<key>/changed per merged attribute + one event/<name> per emitted named event); a once-per-frame guard (visited set within the per-terminal loop + `HasRunForNodeInFrame` probe across terminals) prevents multi-signal fan-out from re-affirming the same receiver. Common pitfalls refreshed to remove lifecycle-handler and last_outcome references.
