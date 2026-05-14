---
concept: invalidate
definition: |
  Rimsky's only graph-level message. Sent to a node, it marks the node `stale`; the cascade walk then traverses the per-template subscription-edge inverse map and stale-marks each receiver that subscribed to the sender's transition.
proto_symbol: (none)
config_field: (none)
api_surface: POST /nodes/{id}/invalidate
related: [cascade, node, node-state, subscription, wait-set]
deprecated_terms: []
---

# Invalidate

## Definition

Rimsky's only graph-level message. Sent to a node, it marks the node `stale`; the cascade walk then traverses the per-template subscription-edge inverse map and stale-marks each receiver that subscribed to the sender's transition.

## Why it exists

Rimsky's reactive model is grounded in a single message because the system needs precisely one verb at the graph level: "this node's value is no longer current." That verb is `invalidate`. Every other propagation effect (recalculation, error handling, schedule firing) reduces to "node X is invalidated; the cascade engine handles the rest."

The single-message design keeps the cascade engine small and auditable. The state machine has five states; the message vocabulary has one entry. Together they specify the entire reactive-propagation semantics.

`invalidate` originates from these places:

1. **Operator-driven**: a `POST /nodes/{id}/invalidate` request from the control API or `rimsky-cli`.
2. **Schedule-driven**: a scheduled fire-time arriving at a node configured with a cron schedule.
3. **Cascade-walk-driven**: the scheduler's cascade walk, evaluating the per-template subscription-edge inverse map at a sender's transition, stale-marks every receiver whose `subscribes:` entry could match (and inserts a wait-set row gating its eligibility until the sender settles).

In all cases the propagation rule is identical: the receiver enters `stale` (idempotent if already stale) and its wait-set row blocks dispatch until the upstream sender resolves.

## `frame: in | next` — per-emit frame discipline

Every invalidate emit declaration carries an optional `frame:` field controlling whether the emit joins the current cascade or buffers a new frame:

- **`frame: in`** (default for per-node subscriptions) — the emit joins the source's current frame, marking the target `stale` with the source's `frame_id` directly. Suitable for in-cascade reactive coupling.
- **`frame: next`** (default for cross-cutting `instance: true` subscriptions and for operator API emits) — the emit goes through `frame.EnqueueOrCoalesce`, producing a new pending frame that runs only after the current frame ends. Useful for cross-cutting reactions that should not surprise the current frame's resolution.

Where it can appear:

- Operator API (`POST /nodes/{id}/invalidate { frame: ... }`).
- Per-subscription `frame:` modifier in a receiver's `subscribes:` entry.

The cascade-walk-driven invalidates are scheduler actions; they obey the receiver-side subscription's per-edge `frame: in | next` setting rather than a sender-side flag.

## How you encounter it

- **Control API**: `POST /nodes/{id}/invalidate` is the operator-facing trigger.
- **Templates**: a receiver declares `subscribes:` entries pointing at the senders whose transitions should invalidate it; substitution refs (`{{nodes.X.attribute.Y}}`, `{{nodes.X.event.Z.<path>}}`) auto-subscribe. The cascade walk at a sender's transition stale-marks every matching subscriber.
- **Errors**: when an executor terminals with `Error{error_class}`, every receiver whose `subscribes:` entry matches `{node: <sender>, on: state, when: failed, error_class: <class>}` is invalidated by the cascade walk.

## Consumer-visible guarantees

- Invalidate is idempotent: an already-`stale` node receiving an `invalidate` stays `stale`; the cascade still walks subscribers (and they too may already be `stale`, in which case the walk is a no-op for them).
- Invalidate does not preempt running work. An in-flight node will run to its terminal state; the invalidate either queues a new frame (`serial_queue`) or joins the pending coalesce (`coalesce`).

## Common mistakes

- Confusing `invalidate` with "abort." Invalidate signals "the value is no longer current"; it does not interrupt or cancel anything mid-flight. Graceful preemption is not part of the model.
- Treating `invalidate` like a function call that returns a result. Invalidate is fire-and-forget; the cascade walks the graph asynchronously and the scheduler picks up newly-stale nodes on the next tick.
- Thinking there's a second message called "recalculate." There isn't. Recalculation is what the scheduler does to a stale node; `invalidate` is the only message that travels between nodes.
- Trying to invalidate a non-existent target. The control-api endpoint returns an error; receivers' `subscribes:` entries are validated at template registration against the template's declared nodes.

## See also

- [`cascade.md`](cascade.md)
- [`subscription.md`](subscription.md)
- [`wait-set.md`](wait-set.md)
- [`node-state.md`](node-state.md)
