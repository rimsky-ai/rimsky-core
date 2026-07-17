---
concept: message-schema
status: as-is
aliases: []
---

# Message-schema

## Definition

A message-schema is the template-level registry of accepted message types for instances of that template. Declared at template top level, parallel to the per-node attributes block and the template's publishers block. Each entry pairs a message type-path with a body shape declared in JSON Schema. The registry is content-addressed into the template's spec at registration.

## Purpose

Give messages a typed contract instead of opaque envelopes. An instance receiving a message of an undeclared type refuses the request with an unknown-type response. The body shape is what receivers substitute from and what message-sender nodes match their attribute schemas against; both surfaces share the same engine.

## Boundaries

Owns: the registry's persisted shape (content-addressed into the template spec), the per-entry fields (the type-path and the body-schema declaration), the registration-time validation pass that checks substitution references against declared types and validates message-sender nodes' attribute schemas against the destination type's body schema, the receipt-time registry lookup gate. Does NOT own: the message envelope (see `concept:message`), the message-sender node-kind (see `concept:message-sender-node`), receiver-side subscription (see `concept:node-subscription`), substitution into bodies (see `concept:attribute`).

## Invariants

- The registry is template-level; entries are content-addressed into the template's spec at registration.
- The type-path is unique across entries in the registry.
- Type-path segments do not contain the substitution-directive segment separator; a segment-internal separator would silently misroute the substitution-directive parser.
- The body-schema declaration is a valid JSON Schema.
- Every template's declared-types set carries an implicit empty-type entry seeded at registration with a null body schema. The implicit entry has no fields and no substitution references can resolve against it; receivers gate on the entry via subscription edges, not via body substitution. An author-declared entry of the empty type is refused at registration as reserved-for-runtime.
- Receipt-time lookup against the registry is the gate: unknown type refuses with an unknown-type response.
- The body-schema is documentation and a registration-time check on substitution references; the actual body bytes are validated at the receiver's dispatch via the existing attribute-validation machinery. The body remains inert at receipt (see invariant: 24).
