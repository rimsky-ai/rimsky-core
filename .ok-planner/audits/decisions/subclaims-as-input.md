---
audit: subclaims-as-input
artifact: decision:subclaims-as-input
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:33:36Z
---

# The dispatch-children primitive consumes acquired sub-claims rather than splitting scopes itself

Supported. The producer's scope-split call has exactly one caller in the runtime, and it sits in the fan-out acquisition helper: that helper resolves the parent claim, substitutes the partition request, calls the producer's split, and returns the acquired sub-claims on the acquisition. A separate one-line converter turns those sub-claims into partition descriptors carrying only a partition key and an already-minted claim-handle id, and the dispatch-children primitive takes that list as input — it creates or reuses a run scope per partition, creates the child runs, and rebinds each carried handle to its new child run, while refusing a partition that carries a handle against more than one child spec. Nothing inside the dispatch primitive resolves a producer or calls split, and the delegation path passes a single partition with no handle at all, so the two surfaces compose without the coupling the rejected alternative would have introduced.
