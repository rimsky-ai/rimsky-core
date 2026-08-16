---
audit: wait-set
artifact: concept:wait-set
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:08:13Z
---

# The per-frame waiting ledger, its gate evaluator, and its ownership claims

Unsupported on three counts, against a body whose other claims hold. The ledger exists with the four-part primary key the concept states, cascade-deleted with its frame, and drain stamps a timestamp on every matching row without deleting any and without discriminating on topic kind. The gate evaluator carries all three conjuncts: an undrained probe over the receiver's own rows, an upstream in-flight probe whose in-flight set is exactly the five states named and which exempts the receiver's own node and a held upstream sharing sub-graph membership, and an advanced-sibling probe over the same node and run scope; it then builds the resolved bag, applies the per-node cascade-mode rule, and transitions the row. Insertion is gated by the subscriber's predicate, evaluated on the walk before any row is written, and non-cascade runs go straight to the dispatchable state with no rows created for them. What fails: first, the concept claims a single insertion path on the cascade walk, and there are three production insertion sites — the cascade walk, the force-upstream-refresh pull, and sub-graph child dispatch, the last of which is not a cascade walk at all. Second, the concept states that the upstream-dependency predicate no longer lives at dispatch time and that the dispatcher's claim checks only the serialisation gate; candidate selection in both storage dialects carries an undrained-wait-set predicate alongside the serialisation predicate. Third, the concept twice says the dispatcher claims runs in sequence order; candidate selection in both dialects orders by enqueue timestamp and then by row id, and never reads the sequence column.
