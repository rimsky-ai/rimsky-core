---
audit: instance
artifact: concept:instance
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:57:08Z
---

# The instance row: identity, queue mode, termination, and its fourteen invariants

Supported. Identity is the generated UUID with a nullable key whose uniqueness is a composite constraint over template hash plus key, so the same key is legal under two templates and idempotent under one — the create path looks the existing row up by that pair and returns it with the rest of the request ignored. The template binding is a foreign key fixed at creation. Candidate selection filters paused rows, with a case for it in the persistence conformance suite against both backends. The queue mode is a two-value property materialized onto the row at creation with the operator's value overriding the template default and any other value rejected at the endpoint; under coalesce the enqueue path cancels the instance's prior pending messages in the same transaction as the insert, for every message type without exception, which is what bounds the pending set at one. Termination is exactly one call site in the whole tree — the operator terminate handler — so the claim that an instance never self-terminates and has no per-frame or per-run auto-termination path is structural rather than conventional; that handler force-fails every run in the shared five-state in-flight list, cancels pending messages, ends every open frame, and stamps the terminal timestamp, and its only precondition is whether the timestamp is already set, reading nothing about subscriptions or nodes. Deletion is refused until the timestamp is set. Instantiation runs the static-config gate before provisioning, validating each node's statically-composed attribute bag against the referenced executor's advertised schema and aborting create on a violation; executor existence is settled earlier, at registration, by the declared-executor hook. Tag substitution at materialization is given a resolve context carrying instance params and nothing else, and a failure aborts the create. The instance row has exactly two mutating statements in each backend — the terminal stamp and the paused flag — both reached only through the control API, so no frame-processing path writes any of the fields the last invariant lists.
