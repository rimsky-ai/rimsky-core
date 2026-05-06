---
concept: invalidate
definition: |
  Rimsky's only graph-level message. Sent to a node, it marks the node `stale` and cascades the same message to dependents. The cascade engine is a pure reachability walk over the dependency graph rooted at the invalidated node.
proto_symbol: (none)
config_field: (none)
api_surface: POST /nodes/{id}/invalidate
related: [cascade, node, node-state]
deprecated_terms: []
---

# Invalidate

## Definition

Rimsky's only graph-level message. Sent to a node, it marks the node `stale` and cascades the same message to dependents. The cascade engine is a pure reachability walk over the dependency graph rooted at the invalidated node.

## Why it exists

Rimsky's reactive model is grounded in a single message because the system needs precisely one verb at the graph level: "this node's value is no longer current." That verb is `invalidate`. Every other propagation effect (recalculation, error handling, schedule firing) reduces to "node X is invalidated; the cascade engine handles the rest."

The single-message design keeps the cascade engine small and auditable. The state machine has four states; the message vocabulary has one entry. Together they specify the entire reactive-propagation semantics.

`invalidate` can originate from three places:

1. **Operator-driven**: a `POST /nodes/{id}/invalidate` request from the control API or `rimsky-cli`.
2. **Schedule-driven**: a scheduled fire-time arriving at a node configured with a cron schedule.
3. **Executor-driven**: an executor reports an `error_class` whose policy chain in the template resolves to the `invalidate(targets)` action. (The executor reports only the error class; the supervisor maps `(error_class, retry_counter)` to the action.)

In all three cases, the propagation rule is identical.

A fourth originator was added in the reactive-loops + lifecycle-handlers spec (May 2026): **lifecycle-handler-driven** invalidate emits. Each of the four lifecycle handler slots may declare an optional `invalidate: { targets: [...], frame: ... }` block that fires unconditionally when the handler runs. See [`node.md`](node.md).

## `frame: in | next` — per-emit frame discipline

Every invalidate emit declaration carries an optional `frame:` field controlling whether the emit joins the current cascade or buffers a new frame:

- **`frame: next`** (default) — the emit goes through `frame.EnqueueOrCoalesce`, producing a new pending frame that runs only after the current frame ends. This preserves today's behavior: cascades don't cross frame boundaries.
- **`frame: in`** — the emit joins the source's current frame, marking the target `stale` with the source's `frame_id` directly. Useful for self-invalidation loops (`{ targets: [self], frame: in }`) that want a single frame to span all iterations.

Where it can appear:

- Operator API (`POST /nodes/{id}/invalidate { frame: ... }`).
- Error-types policy invalidate action (`policy: [{action: invalidate, targets: [...], frame: ...}]`).
- Lifecycle-handler invalidate (`on_*: { invalidate: { targets: [...], frame: ... } }`).

Default is `next` everywhere except the cascade-on-commit and pure-cascade scheduler walks (which are scheduler actions, not configurable invalidate emits).

## How you encounter it

- **Control API**: `POST /nodes/{id}/invalidate` is the operator-facing trigger.
- **Templates**: the `dependencies:` list of each node declaration determines the cascade target set.
- **Error handling**: when an executor's terminal `Errored` event reports an `error_class` and the template's policy chain resolves it to the `invalidate(targets)` action, the named targets are cascaded.

## Consumer-visible guarantees

- Invalidate is idempotent: an already-`stale` node receiving an `invalidate` stays `stale`; the cascade still walks dependents (and they too may already be `stale`, in which case the walk is a no-op for them).
- Invalidate does not preempt running work. An in-flight node will run to its terminal state; the invalidate either queues a new frame (`serial_queue`) or joins the pending coalesce (`coalesce`).

## Common mistakes

- Confusing `invalidate` with "abort." Invalidate signals "the value is no longer current"; it does not interrupt or cancel anything mid-flight. Graceful preemption is not part of the model.
- Treating `invalidate` like a function call that returns a result. Invalidate is fire-and-forget; the cascade walks the graph asynchronously and the scheduler picks up newly-stale nodes on the next tick.
- Thinking there's a second message called "recalculate." There isn't. Recalculation is what the scheduler does to a stale node; `invalidate` is the only message that travels between nodes.
- Trying to invalidate a non-existent target. The control-api endpoint returns an error; the executor's `invalidate(targets)` action references node names that must be present in the same template.

## See also

- [`cascade.md`](cascade.md)
- [`node-state.md`](node-state.md)
