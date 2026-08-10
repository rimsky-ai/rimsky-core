---
audit: commit-response-honored
artifact: story:commit-response-honored
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# The base Commit response's two fields are honored

Supported. A producer written against the base protocol, advertising no
data-processing protocol, returned the same version id and producer-metadata
blob on every Commit, and a stack was pointed at it. The claim handle for an
ordinary claim carries that version id and reads committed, and so does each of
the three sub-claim handles of a fan-out. The fan-out parent's writeback carries
the producer-metadata blob keyed by partition key, its values the base64 of the
exact bytes the producer returned. The run does not observe the last partition's
entry: the node reading the writeback is dispatched by the writeback's own
change signal and no dispatch follows the final commit, so its last snapshot
predates that write — the reachability of a partition's metadata is what the run
establishes, not how many entries the row holds at rest.

## Compliance

The body prescribes mechanism — the two wire fields by name, the claim-handle
row and the fan-out parent's writeback as their destinations — and its benefit
clause states a property of the product's internal consistency ("the fields the
wire contract documents are real for the base protocol, not only for the
data-processing mix-in") rather than something a user gains. Compliant text: "As
a claim-producer author, I can label each committed version and attach my own
details to it, and see both reach the workflows that consume the claim, so that
downstream nodes can tell which version they are looking at without my producer
implementing anything beyond the base protocol."
