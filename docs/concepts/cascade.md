---
concept: cascade
definition: |
  The propagation of `invalidate` through the node graph. When a node loses or replaces its value, downstream dependents are marked `stale` so the scheduler can recalculate them from the new value. Cascade is the reactive-computation engine at the heart of Rimsky.
proto_symbol: (none)
config_field: (none)
api_surface: POST /nodes/{id}/invalidate
related: [node, node-state, invalidate, frame]
deprecated_terms: []
---

# Cascade

## Definition

The propagation of `invalidate` through the node graph. When a node loses or replaces its value, downstream dependents are marked `stale` so the scheduler can recalculate them from the new value. Cascade is the reactive-computation engine at the heart of Rimsky.

## Why it exists

A workflow whose steps depend on each other faces a natural problem: when an upstream input changes, every downstream step that consumed it is now stale. Rimsky models this directly. Each node declares its dependencies; when a node's value changes (or is explicitly invalidated), the cascade engine walks the dependency graph and marks every dependent stale. The scheduler picks up stale nodes on subsequent ticks and recalculates them by dispatching their executors.

`invalidate` is Rimsky's only graph-level message. Recalculation is not a service message — it's a scheduler action. The dispatch loop sees a node in `stale`, checks eligibility (claims and locks acquirable, dependencies fresh), and runs the node's executor. The clean separation lets the cascade engine be a pure reachability computation while the scheduler handles all the I/O concerns (claim acquisition, lock contention, executor dispatch, async callbacks).

A cascade always happens in the context of a frame. The frame ID stamps every cascade-affected row, so observability tooling can answer "show me everything that happened as a result of this invalidate."

### The cascade-firing gate (lazy + last_outcome-driven)

Today's lazy + Changed-gated cascade is preserved end-to-end. When a node commits, the supervisor's terminal handler fires the cascade only when the commit produced new value — i.e., the upstream's `last_outcome == fresh_changed`. Under the default `on_executor_complete: { resolve: by_changed }` (also the implicit default when no handler is declared) this is functionally identical to the prior `t.Changed` gate.

`last_outcome` is **observability metadata, not a dispatch gate**. Two non-default `on_executor_complete` resolutions diverge from the default:

- `always_propagate` forces `last_outcome=fresh_changed` even on `changed:false`, so the cascade fires.
- `never_propagate` forces `last_outcome=fresh_unchanged` even on `changed:true`, so the cascade does not fire.

Per-emit invalidate frame discipline (`frame: in | next`) is documented in [`invalidate.md`](invalidate.md).

## How you encounter it

- **Control API**: `POST /nodes/{id}/invalidate` triggers a cascade rooted at the named node.
- **Templates**: the `dependencies:` list of each node declaration determines what propagates where.
- **Errors**: when the supervisor's policy chain resolves an executor-reported `error_class` to the `invalidate(targets)` action, the named targets are cascaded as part of the failure-handling path.

## The `changed` halt-at-this-node signal

A node's executor commits with a `changed: bool` declaration. `changed: true` (the default) propagates `invalidate` to dependents; `changed: false` halts propagation at this node — dependents are not awakened. The producer is trusted to declare honestly; Rimsky does not byte-compare values.

## Consumer-visible guarantees

- Cascade respects in-flight work: a `running` node is not preempted by an upstream invalidate. The invalidate either enqueues a new frame (`serial_queue` mode) or joins the pending coalesce (`coalesce` mode), and only takes effect once the current run terminates.
- Cascade is deterministic: given an identical dependency graph and an identical sequence of root invalidates, the set of affected nodes is the same.

## Common mistakes

- **Rimsky's cascade ≠ CSS cascade.** CSS's cascade resolves competing style rules by specificity and order; Rimsky's cascade propagates `invalidate` through a directed acyclic dependency graph.
- Treating "recalculate" as a second message. There is one cascade message: `invalidate`. Recalculation is what the scheduler does next, not a service message that travels alongside.
- Expecting cascade to skip nodes whose new value would be byte-identical to the old. Cascade is dependency-driven, not value-diff-driven; the executor commits `changed: false` if it wants to halt propagation at itself.
- Confusing cascade reach with executor invocation. Cascade marks nodes stale; the scheduler decides which stale nodes are eligible for dispatch (after acquiring any required claims and locks).

## See also

- [`node-state.md`](node-state.md)
- [`invalidate.md`](invalidate.md)
- [`frame.md`](frame.md)
