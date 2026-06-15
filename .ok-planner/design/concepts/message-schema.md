---
concept: message-schema
status: as-is
aliases: []
---

# Message-schema

## Definition

A message-schema is the template-level registry of accepted message types for instances of that template. Declared in a `messages:` block at template top level, parallel to the `attributes:` block on a node and the `publishers:` block at template level. Each entry pairs a message type-path with a body shape declared in JSON Schema. The registry is content-addressed into the template's spec at registration.

## Purpose

Give messages a typed contract instead of opaque envelopes. An instance receiving a message of an undeclared type refuses the request with an unknown-type response. The body shape is what receivers substitute from and what message-emitter nodes match their attribute schemas against; both surfaces share the same engine.

## Boundaries

Owns: the registry's persisted shape (content-addressed into the template spec), the per-entry fields (`type:`, `body_schema:`), the registration-time validation pass that checks substitution references against declared types and validates message-emitter nodes' attribute schemas against the destination type's body schema, the receipt-time registry lookup gate. Does NOT own: the message envelope (see `concept:message`), the message-emitter node-kind (see `concept:message-emitter-node`), receiver-side subscription (see `concept:node-subscription`), substitution into bodies (see `concept:attribute`).

## Invariants

- The registry is template-level; `messages:` entries are content-addressed into the template's spec at registration.
- `type:` is unique across entries in the registry.
- `type:` segments do not contain `.`; the substitution-directive parser splits on `.` and a segment-internal `.` would silently misroute.
- `body_schema:` is a valid JSON Schema.
- `body_schema:` must not declare the reserved top-level property `wake_node_ids`; this is the runtime-synthetic wake mechanism's field name, structurally guarded at registration (template) and at receipt (operator and publisher emits) so an author-declared envelope cannot smuggle stale-mark targets through the runtime wake path. The reservation is wire-name-only: publishers who happen to carry domain data of that name must pick another field name.
- Receipt-time lookup against the registry is the gate: unknown type refuses with an unknown-type response.
- The body-schema is documentation and a registration-time check on substitution references; the actual body bytes are validated at the receiver's dispatch via the existing attribute-validation machinery. The body remains inert at receipt (see `@blessed-invariant: 21`).
