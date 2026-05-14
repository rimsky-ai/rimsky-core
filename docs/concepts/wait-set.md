---
concept: wait-set
definition: |
  A per-frame ledger that records "receiver R is waiting for sender S in frame F under (topic_kind, subscription_scope)." Cascade walks insert rows pessimistically when senders transition; the settled-state drain bulk-deletes rows when senders resolve. A stale node is dispatch-eligible iff its wait-set is empty in the current frame.
proto_symbol: (none)
config_field: (none)
api_surface: GET /admin/diagnostics/wait-sets
related: [subscription, cascade, frame, node-state]
deprecated_terms: []
---

# Wait-set

## Definition

A per-frame ledger that records "receiver R is waiting for sender S in frame F under (topic_kind, subscription_scope)." Cascade walks insert rows pessimistically when senders transition; the settled-state drain bulk-deletes rows when senders resolve. A stale node is dispatch-eligible iff its wait-set is empty in the current frame.

## Why it exists

The dispatch eligibility predicate used to read "all dependencies fresh" — a strict-AND over the per-node `dependencies:` list. Under the subscription model, there is no static dependency list to scan; coupling is declared per-topic and the cascade walk announces it at run-time. The wait-set ledger derives eligibility from cascade history:

- The cascade walk inserts a row whenever a sender's transition could match a receiver's subscription.
- The settled-state drain bulk-deletes rows when the sender reaches a settled state (`fresh`, `failed`, `parked`).
- The scheduler's `SweepReady` query dispatches stale nodes whose wait-set is empty for the current frame.

Idempotent re-fire handles the "filter didn't actually match" case: every cascade-walk match inserts a row regardless of filter compatibility; the settled-state drain releases the gate uniformly. Receivers whose filter did not match at the settled state still re-dispatch — they produce equivalent output because their attribute substitutions resolve from the unchanged upstream value.

## How rows are created and drained

- **Insert on cascade walk**: when a sender transitions out of a settled state, the cascade engine reads the per-template inverse-edge map for the sender's node-type, finds every subscription edge that could match at the sender's eventual settled state (state subscriptions regardless of `when:` filter; attribute subscriptions; named-event subscriptions), marks each receiver `stale` (idempotent within the frame), and inserts a wait-set row for each match.
- **Insert on named-event emission**: when a sender emits a named event mid-cycle, matching `on: event` subscribers also receive a wait-set row + stale-mark.
- **Drain on settle**: when a sender reaches `fresh`, `failed`, or `parked`, the engine bulk-deletes wait-set rows where `frame_id = F AND sender_node_id = S`. The drain is unconditional across topic kinds.
- **Drop on frame close**: when a frame closes, its wait-set rows are removed atomically with the frame. Stale rows from prior frames cannot affect new frames.

## Cross-cutting vs per-node

A receiver with both a per-node subscription and a cross-cutting (`instance: true`) subscription that match the same sender on the same topic kind gets two rows; both must drain before the receiver is eligible. The two scopes are distinguished by the `subscription_scope` column (`direct` for per-node, `instance` for cross-cutting).

## Eligibility predicate

```
A stale node is dispatch-eligible iff no wait-set row exists for
(frame = current_frame, receiver = node).
```

A node with no upstream-graph invalidators this frame (e.g. operator-API direct invalidate, scheduled-node cron tick, freshly-created node with no in-graph triggers) has an empty wait-set and is immediately eligible.

## How you encounter it

- **Diagnostics**: `GET /admin/diagnostics/wait-sets?frame=<frame_id>&node=<node_id>` returns the wait-set rows for a given frame (and optionally narrowed to one receiver). Used for debugging stuck frames.

## Consumer-visible guarantees

- A stale receiver is eligible iff its wait-set is empty for the current frame.
- The bulk-delete-on-settle rule covers every topic kind uniformly (idempotent re-fire when a filter didn't actually match).
- Wait-set rows live only within their frame's lifetime; no stale ledger entries survive frame close.
- Multiple invalidators are handled trivially — each sender contributes its own row; the receiver waits until all rows for `(frame, receiver)` are gone.

## Common mistakes

- Expecting wait-set rows to persist across frames. They do not — frame close cascade-deletes them.
- Reading the wait-set as the "subscription graph." It is not — it is per-frame run-time history. The static coupling is the per-template inverse-edge map computed at registration.
- Assuming a wait-set row gates on filter match. The row is pessimistic; the drain is unconditional on sender settle. Receivers whose filter didn't actually match still re-dispatch idempotently.

## See also

- [`subscription.md`](subscription.md)
- [`cascade.md`](cascade.md)
- [`frame.md`](frame.md)
- [`node-state.md`](node-state.md)
