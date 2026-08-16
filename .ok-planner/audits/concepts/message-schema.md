---
audit: message-schema
artifact: concept:message-schema
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:15:12Z
---

# The template-level message-type registry: seven invariants from registration validation to the ledger chokepoint

Supported. All seven invariants hold. The registry is a top-level block on the template spec, so it is content-addressed with everything else the template hash covers. Registration validation refuses a duplicate type-path, refuses a path with an empty segment, refuses a segment containing the substitution-directive separator with an error saying why, refuses a path bearing no separator at all so it cannot collide with a node type, refuses one colliding with a declared node type, and compiles each declared body schema — rejecting non-JSON, a non-object shape, and a schema that fails to compile as three distinct errors. An author-declared empty-type entry is refused as reserved-for-runtime, and every declared-types set is built with the empty entry added, so the implicit entry exists for every template without being author-writable. Receipt-time lookup is the gate: the send endpoint builds the declared set including the empty type, and an unmatched type returns a bad-request naming the type and listing the declared and implicit types. Body validation sits at one chokepoint — the single enqueue function every production insertion passes through, reached from the operator and publisher endpoint and from a message-sender node's dispatch alike; the only other insert calls are test seeds. A violation is a typed error naming the type, carrying the schema library's field-level detail, and it aborts before the row lands. Delivery reads the ledger and runs no second validation pass.
