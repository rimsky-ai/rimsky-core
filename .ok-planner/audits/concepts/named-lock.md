---
audit: named-lock
artifact: concept:named-lock
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:34Z
---

# Named locks as producer-independent capacity counters, and the five invariants their concept claims

Supported. The lock configuration carries exactly one field, a per-name integer limit rejected below one at config validation, so the mutex-versus-semaphore distinction is inferred from the value rather than declared — matching the concept's account of the shape. All five invariants hold against the code as it stands: the named-lock spec and the claim spec are separate types with no shared interface, carried in an untyped slice and dispatched by a type switch at the one acquisition entry point; every acquisition sorts the whole spec slice by kind then by sort key before any lock is taken, and a lock-ordering scenario test asserts that ordering; the capacity count is a single-table count over the claim-handle ledger filtered to the named kind, the name, and the active state in both persistence backends, with a shared persistence-conformance case asserting a committed row stops occupying capacity; the count-then-insert runs after a per-name advisory lock taken in the same transaction (a Postgres transaction-scoped advisory lock, and on SQLite the immediate write transaction that serialises all writers, with its own race test); and a template's lock name resolves through the ordinary substitution grammar at acquisition, with directive-free names checked at registration against the operator-declared set and a substitution failure routed to the node's substitution-failure error policy under a dedicated site label, each covered by unit tests. One behaviour the concept does not settle: when a substituted lock name resolves to a name the operator never declared, acquisition proceeds with no capacity ceiling at all.
