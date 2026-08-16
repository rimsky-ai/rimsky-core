---
audit: child-execution
artifact: concept:child-execution
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T04:47:34Z
---

# The two child-execution mechanisms, their shared dispatch helper, and the six invariants the concept claims

Unsupported: the first invariant's universal is false. It claims settlement — carry or aggregate — is the only run-side path that closes child execution contexts, with administrative instance termination as the sole exception. Enumerating every production call site that closes a run scope gives four (two more sit only in persistence-conformance test helpers): the carry settle, the aggregate settle, the instance-termination sweep the concept names, and a fourth the concept does not — the frame engine's settled-frame scope-tree sweep, which walks the frame's whole scope tree deepest-first, closes every scope still open, fires each closed scope's run-scope-terminal peer fan-out, and logs a distinct warning when the scope it is closing is a child rather than the frame root. That is a run-side path closing child contexts outside settlement and outside the named exception. The concept's other five invariants hold: fan-out passes the shared helper N partitions by one child spec built from the calling node's own row while delegation passes one partition by the sub-graph's internal nodes with the entry filtered out; a sub-graph's exit is a single template field, so at-most-one-exit is grammatical rather than runtime-checked, and the carry settle fires on that exit's terminal; entry absorption is computed in template canonicalisation and reaches the helper only as a boolean it uses for recursion rejection; the parent-settlement cascade bridge fires inside each settle primitive, directly for carry and via the held-claim deferred cascade inside the claim-terminal resolution the aggregate calls; and each settle's outcome carry and its scope closure share one transaction. Per-clone attribute writebacks are not aggregated onto the parent bag — the only child-sourced write to the parent row is producer commit metadata under a namespaced key, which is not an attribute delta.
