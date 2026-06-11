---
story: cascade-signal-blind
status: as-is
---

# Template author wires reactive nodes against any cascade-firing signal type

## Role

As a template author wiring reactive nodes, I can subscribe to any cascade-firing signal type the runtime emits — `terminal/success`, `terminal/error/<class>`, `transient/retry/<n>/<class>`, `attribute/<key>/changed`, `event/<name>` — and have my subscriber dispatched when a matching signal lands, regardless of which type it is, so that I write "react to X" topologies without learning which signal types are first-class and which are quietly second-class.

## Capability

The cascade engine is signal-blind. Subscription firing is gated purely on `(edge type-path match) AND (CEL when: predicate)`; the engine never branches on the signal type itself. Equivalently: the same code path that delivers `terminal/success` to its subscribers delivers `terminal/error/<class>`, `transient/retry/<n>/<class>`, `attribute/<key>/changed`, and `event/<name>` to theirs. A new cascade-firing signal type added to the canonical taxonomy automatically becomes observable.

## Business value

Template authors write "react to X" without learning which signal types are first-class and which are quietly second-class. The "react to upstream error" topology — a deterministic-primary node `terminal/error`s on cache-miss, paired with a repair node subscribing to its `terminal/error/*` — composes the same way as the "react to upstream success" topology.

## Acceptance

For each cascade-firing signal type in the canonical taxonomy, a node declaring a subscription with a matching type-path dispatches when the upstream emits the signal. Exact-type and trailing-`*` prefix subscription shapes both fire. Per-sender (`{ node: X, type: ... }`) and cross-cutting (`instance: true`) subscription shapes both fire. The signal's audit row lands in the event log on every emit, so an operator's event-log query and a subscriber's wait-set see the same signal. Concretely: a per-sender `{ node: X, type: terminal/error/* }` subscription fires when the sender settles `terminal/error/<class>` via either `error_types: give_up` (failed-color settlement) or `error_types: pass` (fresh-color settlement).

## Falsifier

Any single cascade-firing signal type produces no subscriber dispatch when its subscription matches the type-path, OR the event-log audit row for that emit is missing, OR the per-sender `terminal/error/*` subscriber doesn't dispatch when the sender settles `terminal/error/<class>`.

## Proof

Executable proof — table-driven scenario test that iterates over the cascade-firing signal types and asserts, for each: (a) a per-sender subscription on that type-path dispatches its subscriber when the upstream emits the signal; (b) a cross-cutting (`instance: true`) subscription on that type-path dispatches; (c) the audit row for the signal lands in the event log; (d) trailing-`*` prefix subscription shapes match every type-path with that prefix. Pins `concept:cascade` invariant "Cascade fires iff a subscription edge matches the emitted signal's type AND the subscriber's CEL when: predicate evaluates true." The non-cascading signal types — `terminal/park/<reason>` and `terminal/infra/<class>` — are explicitly out of the proof's scope per their design (those emit a bare audit row, no cascade, because the node resumes rather than settling).
