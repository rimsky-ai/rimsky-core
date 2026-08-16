---
audit: claimant-guard-helper
artifact: decision:claimant-guard-helper
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:30:00Z
---

# One claimant-guard predicate definition per storage driver, no copies

Supported, and checked by enumeration rather than by sampling. Each of the two storage drivers defines the guard predicate exactly once — one placeholder-numbering helper function in the client-server driver, one clause constant in the embedded driver — and each is referenced by thirteen claimant-guarded mutations, including the holder reassignment, which guards on the outgoing holder. Sweeping every occurrence of the holder column across both drivers' non-test sources accounts for all of them: the two definitions, the twenty-six uses, the shared column lists, one assignment target in the reassignment update, the unheld-handle predicates that test the column for null rather than for a claimant, and one observability filter that admits a null argument as "any holder". No hand-written copy of the guard predicate exists outside the two definitions. Both rejected alternatives are absent: there is no cross-driver query builder, and no mutation site writes the predicate inline.
