---
concept: cascade
definition: |
  The propagation of `invalidate` through the node graph. When a node loses or replaces its value, every receiver that subscribed to the sender's transition is marked `stale` so the scheduler can recalculate them from the new value. Cascade is the reactive-computation engine at the heart of Rimsky.
proto_symbol: (none)
config_field: (none)
api_surface: POST /nodes/{id}/invalidate
related: [node, node-state, invalidate, frame, subscription, wait-set]
deprecated_terms: []
---

# Cascade

## Definition

The propagation of `invalidate` through the node graph. When a node loses or replaces its value, every receiver that subscribed to the sender's transition is marked `stale` so the scheduler can recalculate them from the new value. Cascade is the reactive-computation engine at the heart of Rimsky.

## Why it exists

A workflow whose steps depend on each other faces a natural problem: when an upstream input changes, every downstream step that consumed it is now stale. Rimsky models this directly. Each node declares the upstream topics it subscribes to (and substitution refs auto-subscribe — see [`subscription.md`](subscription.md)). When a node's value changes (or is explicitly invalidated), the cascade engine consults the per-template subscription-edge inverse map and marks every matching receiver stale. The scheduler picks up stale nodes on subsequent ticks and recalculates them by dispatching their executors.

`invalidate` is Rimsky's only graph-level message. Recalculation is not a service message — it's a scheduler action. The dispatch loop sees a node in `stale`, checks eligibility (claims and locks acquirable, dependencies fresh), and runs the node's executor. The clean separation lets the cascade engine be a pure reachability computation while the scheduler handles all the I/O concerns (claim acquisition, lock contention, executor dispatch, async callbacks).

A cascade always happens in the context of a frame. The frame ID stamps every cascade-affected row, so observability tooling can answer "show me everything that happened as a result of this invalidate."

### The cascade-firing gate (lazy + last_outcome-driven)

Today's lazy + Changed-gated cascade is preserved end-to-end. When a node commits, the supervisor's terminal handler fires the cascade only when the commit produced new value — i.e., the upstream's `last_outcome == fresh_changed`. Under the default `on_executor_complete: { resolve: by_changed }` (also the implicit default when no handler is declared) this is functionally identical to the prior `t.Changed` gate.

`last_outcome` is **observability metadata, not a dispatch gate**. Two non-default `on_executor_complete` resolutions diverge from the default:

- `always_propagate` forces `last_outcome=fresh_changed` even on `changed:false`, so the cascade fires.
- `never_propagate` forces `last_outcome=fresh_unchanged` even on `changed:true`, so the cascade does not fire.

Per-emit invalidate frame discipline (`frame: in | next`) is documented in [`invalidate.md`](invalidate.md).

## Eligibility via the wait-set

Stale-marking a receiver is half of cascade; the other half is the eligibility gate. Each cascade-walk match inserts a row into the per-frame wait-set ledger (see [`wait-set.md`](wait-set.md)) keyed on `(frame, receiver, sender, topic_kind, scope)`. The scheduler's dispatch query reads:

```
A stale node is dispatch-eligible iff its wait-set is empty in the current frame.
```

When the sender resolves to a settled state (`fresh`, `failed`, `parked`), the engine bulk-deletes wait-set rows for that sender in the frame. Receivers with no remaining rows are dispatch-eligible.

## How you encounter it

- **Control API**: `POST /nodes/{id}/invalidate` triggers a cascade rooted at the named node.
- **Templates**: each node's `subscribes:` block (plus the substitution refs in its attribute schema, which auto-subscribe) defines what propagates where.
- **Errors**: when a node terminals with `Error{error_class}`, every receiver whose `subscribes:` entry matches `{node: <sender>, on: state, when: failed, error_class: <class>}` is stale-marked as part of the failure-handling path.

## The `changed` halt-at-this-node signal

A node's executor commits with a `changed: bool` declaration. `changed: true` (the default) propagates `invalidate` to dependents; `changed: false` halts propagation at this node — dependents are not awakened. The producer is trusted to declare honestly; Rimsky does not byte-compare values.

## Consumer-visible guarantees

- Cascade respects in-flight work: a `running` node is not preempted by an upstream invalidate. The invalidate either enqueues a new frame (`serial_queue` mode) or joins the pending coalesce (`coalesce` mode), and only takes effect once the current run terminates.
- Cascade is deterministic: given an identical dependency graph and an identical sequence of root invalidates, the set of affected nodes is the same.

## Common mistakes

- **Rimsky's cascade ≠ CSS cascade.** CSS's cascade resolves competing style rules by specificity and order; Rimsky's cascade propagates `invalidate` through the per-template subscription-edge inverse map.
- Treating "recalculate" as a second message. There is one cascade message: `invalidate`. Recalculation is what the scheduler does next, not a service message that travels alongside.
- Expecting cascade to skip nodes whose new value would be byte-identical to the old. Cascade is subscription-driven, not value-diff-driven; the executor commits `changed: false` if it wants to halt propagation at itself.
- Confusing cascade reach with executor invocation. Cascade marks nodes stale and inserts wait-set rows; the scheduler decides which stale nodes are eligible for dispatch (wait-set empty for the current frame, claims and locks acquirable).

## See also

- [`node-state.md`](node-state.md)
- [`invalidate.md`](invalidate.md)
- [`frame.md`](frame.md)
- [`subscription.md`](subscription.md)
- [`wait-set.md`](wait-set.md)
