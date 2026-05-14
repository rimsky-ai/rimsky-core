---
concept: attributes
definition: |
  The typed inputs and outputs of a node, declared by a JSON Schema in the template's `attributes:` block. Attributes are the substitution boundary: `{{nodes.<source>.attribute.<field>}}`, `{{nodes.<source>.event.<name>.<path>}}`, `{{claim.<alias>.<path>}}`, `{{params.<key>}}` directives in the schema's `source:` fields are resolved at dispatch.
proto_symbol: (none)
config_field: (none)
api_surface: (none)
related: [node, claim, inheritance, userdata, subscription]
deprecated_terms: []
---

# Attributes

## Definition

The typed inputs and outputs of a node, declared by a JSON Schema in the template's `attributes:` block. Attributes are the substitution boundary: `{{nodes.<source>.attribute.<field>}}`, `{{nodes.<source>.event.<name>.<path>}}`, `{{claim.<alias>.<path>}}`, `{{params.<key>}}` directives in the schema's `source:` fields are resolved at dispatch.

## Why it exists

Nodes need typed contracts. Without them, dependency edges between nodes are wire-shaped guesses; the value an executor receives could be anything. Rimsky declares each node's inputs and outputs as a JSON Schema; the supervisor enforces the schema at two points:

1. **At dispatch (post-substitution)**: after resolving `{{...}}` directives, the resolved input attributes are validated against the schema. A validation failure rejects the dispatch with a clear error.
2. **At commit (executor writeback)**: when the executor returns its terminal `Complete` event, the writeback is validated against the schema before being persisted as the node's value.

Both gates are mandatory. The double-validation catches both substitution bugs (where the source value doesn't match the schema) and executor bugs (where the executor returns something the schema doesn't allow).

The schema's `source:` field is where substitution happens. Each property declares where its value comes from — typically `nodes.<source>.attribute.<key>` for upstream attribute writeback, `nodes.<emitter>.event.<name>.<path>` for executor-emitted named-event payloads, `claim.<alias>.payload.<path>` for claim-pass payloads, or `params.<path>` for instance-level params. The schema is standard JSON Schema (Draft 7+); `source:` is a Rimsky-specific extension that names the substitution.

The `nodes.<emitter>.event.<name>.<path>` source kind reads from the per-instance event ledger (rows persisted when the emitting executor streams a `NamedEvent`). The most-recent emission of `(emitter, name)` is selected; `<path>` walks the JSON payload via the same `walkPath` mechanism as the value substitution. Event payloads inherit the same opacity discipline as attribute values — the supervisor never logs, normalizes, or transforms them outside this substitution-leaf extraction (`@blessed-invariant 11` / `@blessed-invariant 21`).

## Substitution refs auto-subscribe

Every substitution directive that references another node implicitly adds a subscription on the consuming node (see [`subscription.md`](subscription.md)):

- `{{nodes.X.attribute.Y}}` adds `{node: X, on: attribute, name: Y}`.
- `{{nodes.X.event.Z.<path>}}` adds `{node: X, on: event, name: Z}`.
- `{{claim.<alias>.<path>}}` and `{{params.<path>}}` are not graph nodes and add no subscription.

This means a node that reads upstream attributes is automatically wired into the cascade — there is no separate `dependencies:` declaration to keep in sync with the substitution refs.

## How you encounter it

- **Templates**: the `attributes:` block of each node declaration.
- **Substitution**: directives like `{{nodes.upstream-node.attribute.value}}` or `{{claim.snapshot.payload.item_id}}` resolve at dispatch.
- **Executor protocol**: the executor receives substituted attributes in `ExecuteRequest`; on `Complete`, the executor returns writeback that becomes the node's persisted value (after schema validation).

## Consumer-visible guarantees

- Attributes are validated at dispatch (post-substitution, on inputs) and again at commit (on writeback). Both gates are mandatory. A node never sees an unsubstituted directive at runtime; an executor's writeback never violates the schema without being rejected.
- A property declared by JSON Schema is the property name an executor sees in `ExecuteRequest` and writes back in `Complete`. The substitution is invisible to the executor — it sees resolved values.

## Common mistakes

- Confusing attributes with userdata. Attributes are typed and substituted; userdata is opaque bytes that rimsky never inspects, parses, substitutes, or validates.
- Expecting executors to receive the unsubstituted source declarations. The supervisor performs substitution before the executor sees any payload; the executor's input is the resolved values.
- Treating commit-time validation failures as transient. The executor returned something invalid; the node fails. Retry behavior is governed by the executor's error action, not by automatic re-validation.

## See also

- [`node.md`](node.md)
- [`claim.md`](claim.md)
- [`inheritance.md`](inheritance.md)
- [`userdata.md`](userdata.md)
- [`subscription.md`](subscription.md)
