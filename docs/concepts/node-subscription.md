---
concept: subscription
definition: |
  A receiver-side declaration that says "fire me when this upstream topic transitions." Three topic kinds: `state` (any node-state transition), `attribute` (an upstream node's attribute writeback), `event` (a named-event emission). Declared per node under `subscribes:` in the template DSL; substitution refs in a node's attribute schema auto-subscribe.
proto_symbol: (none)
config_field: (none)
api_surface: (none)
related: [cascade, wait-set, invalidate, node, attributes]
deprecated_terms: []
---

# Subscription

## Definition

A receiver-side declaration that says "fire me when this upstream topic transitions." Three topic kinds: `state` (any node-state transition), `attribute` (an upstream node's attribute writeback), `event` (a named-event emission). Declared per node under `subscribes:` in the template DSL; substitution refs in a node's attribute schema auto-subscribe.

## Why it exists

The pre-2026-05-14 `dependencies: [foo]` block bundled three independent capabilities into one declaration: (a) read access to `foo`'s attributes via substitution, (b) reactive coupling — when `foo` terminals, I go stale, (c) eligibility gate — don't dispatch me until `foo` is fresh. The three are conceptually distinct. Subscriptions decompose the bundle:

- Read access lives in the substitution grammar (`{{nodes.X.attribute.Y}}`, `{{nodes.X.event.Z.<path>}}`).
- Reactive coupling lives in subscriptions (explicit `subscribes:` plus implicit auto-subscribe from substitution refs).
- Eligibility gating lives in the wait-set ledger (see [`wait-set.md`](wait-set.md)).

This decoupling also retires the send-side `invalidate.targets:` slot on lifecycle handlers and the `action: invalidate` clause on error-types policies. Cascade flow is impactee-declared: each node's reactive surface reads cleanly from its own template entry.

## Topic kinds

| `on:` | Required filters | Optional filters | Fires when |
|---|---|---|---|
| `state` | — | `when: <node-state>`, `outcome: <last-outcome>`, `error_class: <class>`, `reason: <park-reason>` | Subscribed node enters the filtered state. Filters compose conjunctively. Empty `when:` means any state transition. |
| `attribute` | — | `name: <attribute-key>` | Subscribed node terminals with a changed attribute. With `name:`, fires only when that key changed; without, fires on any attribute change. |
| `event` | `name: <event-name>` | — | Subscribed node emits the named event. The name must appear in the upstream executor's `Capabilities.declared_events` (validated at registration when reachable). |

## Scope

- **Per-node** (`node: <upstream>`): the subscription matches only transitions of the named upstream node. Default `frame: in`.
- **Cross-cutting** (`instance: true`): the subscription matches transitions of any node in the instance whose topic satisfies the filters. Useful for "clean up on any failure of class X" patterns. Default `frame: next`.

## Frame modifier

Each subscription accepts an optional `frame: in | next` modifier:

- `frame: in` — the cascade walk's stale-mark + wait-set insert lands in the current frame.
- `frame: next` — the receiver is queued as a frame source for the next frame; the new frame opens after the current one closes.

## Auto-subscribe from substitution refs

Every substitution directive in a node's `attributes` schema that references another node implicitly adds a subscription:

- `{{nodes.X.attribute.Y}}` → `{node: X, on: attribute, name: Y}`.
- `{{nodes.X.event.Z.<path>}}` → `{node: X, on: event, name: Z}`.
- `{{claim.<alias>.<path>}}` and `{{params.<path>}}` add no subscription (not graph nodes).

The auto-subscribed set is unioned with the explicit `subscribes:` block. Explicit declarations exist for couplings that don't read upstream data — e.g. "fire me when X reaches `parked` even though I don't read any of X's attributes."

## DSL example

```yaml
nodes:
  - type: finalize
    executor: http-node
    subscribes:
      - { node: spine_z, on: state, when: fresh, outcome: fresh_changed }
      - { node: optional_check_1, on: state, when: fresh }
      - { node: optional_check_2, on: state, when: fresh }
      - { node: intake, on: event, name: applicable_subgraphs_decided }
      - { node: foo, on: attribute, name: my_attr }
      - { instance: true, on: state, when: failed, error_class: rate_limited, frame: next }
    attributes:
      foo: { source: "{{nodes.intake.event.applicable_subgraphs_decided.foo}}" }
      bar: { source: "{{nodes.spine_z.attribute.bar}}" }
```

The `dependencies:` block does not exist on the new template shape.

## How you encounter it

- **Templates**: the `subscribes:` block on each node declaration; substitution directives in `attributes.<field>.source`.
- **Diagnostics**: `GET /admin/diagnostics/wait-sets?frame=<frame_id>&node=<node_id>` shows the per-frame wait-set rows that drive a receiver's eligibility.

## Consumer-visible guarantees

- Subscriptions are validated at template registration: `node:` references resolve to declared template nodes; topic-kind required filters are present; per-topic-kind output topology is cross-checked against the upstream executor's `Capabilities` (silent-skip when unreachable, mirroring the existing `validateOnEvent` behavior).
- Substitution refs auto-subscribe — no orphan reads. Reading `{{nodes.X.attribute.Y}}` implies a `{node: X, on: attribute, name: Y}` subscription.
- A stale receiver dispatches iff its wait-set is empty for the current frame.

## Common mistakes

- Declaring a `subscribes:` entry for a coupling that the substitution grammar already covers. The auto-subscribed set handles `{{nodes.X.<topic>.<key>}}` references; explicit entries are only needed for non-reading coupling.
- Filtering on `outcome:` when `on:` is not `state`. State-only filters apply only to `on: state`; on `on: attribute` or `on: event` they are rejected at registration.
- Assuming a `dependencies: [foo]` receiver gates only on `foo` reaching `fresh`. Under the new model, the wait-set drain fires on any settled state of `foo` (`fresh`, `failed`, `parked`). Templates that need the old "won't fire if upstream failed" gate must rewrite the coupling as an explicit filtered subscription — e.g. `{node: foo, on: state, when: fresh, outcome: fresh_changed}`.

## See also

- [`cascade.md`](cascade.md)
- [`wait-set.md`](wait-set.md)
- [`invalidate.md`](invalidate.md)
- [`attributes.md`](attributes.md)
