---
audit: run-scope-is-per-frame
artifact: decision:run-scope-is-per-frame
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:29:03Z
---

# A frame owns its root RunScope, and the two nested kinds hang off it

Supported. There are exactly two RunScope creation sites in production code. The frame-open path creates the root scope with no parent, an empty partition key, and the main graph name, and inserts it in the same transaction that inserts the frame row — the frame insert takes the new scope id as an argument, so the two cannot come apart. The child-dispatch path creates the other two kinds from one call: a sub-graph scope carrying the child graph name and a fan-out partition scope carrying a non-empty partition key, both with the parent scope id and the parent node-run id set from the dispatching run. The frame row's root-scope column is declared not-null in both persistence drivers and carries a foreign key to the scope table, and the frame-prune path deletes scopes no frame references. Reads take the root from the frame, not the instance: message delivery reads the frame row's root scope, and both drivers derive a new node-run's scope and its per-node sequence from the frame row in SQL. Frame settlement closes the scope tree rooted at that frame's root. A cross-driver conformance test asserts each frame carries the root scope it was given and that the column is immutable afterwards; the per-frame root accessor is used across roughly four dozen scenario assertions. No production path creates a per-instance scope — the only such naming left is in the persistence conformance fixtures.
