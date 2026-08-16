---
audit: claim-lifetime
artifact: concept:claim-lifetime
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:46:57Z
---

# The two-value claim lifetime and the five rules that distinguish durable from subgraph

Supported. The lifetime selector is a two-value constrained column on the claim-handle ledger surfaced from the template, and every rule the concept states about it is enforced in a query rather than in prose. The retention sweep deletes settled rows past the trailing cutoff but restricts the committed half to the subgraph lifetime, so committed-durable rows survive it indefinitely; the operator asset-delete path and instance deletion are the only two paths that remove them, both enqueueing the producer's release verb on the terminal-verb outbox and then dropping the row through the absence-guarded settled-row delete. Instance deletion is refused until the instance carries a termination timestamp, and it releases every committed-durable handle of the instance without filtering on the held marker; instance termination on its own touches only still-active in-flight claims. The orphan reaper's row-discovery query filters to active rows, so the expiry timestamp governs nothing else. The parent claim's auto-terminal counts committed and abandoned children alike toward the expected total and draws no distinction by lifetime, so any settled child stops blocking the parent. Conflict detection admits active rows plus committed-durable rows and excludes committed-subgraph, and both halves of that split have their own case in the persistence conformance suite, run against both backends. The asset surface is where the data-processing requirement bites: the listing and version-history routes filter producers by an advertised-protocol predicate, so a durable claim against a producer that does not advertise it persists as a durable row without being presented as an asset — which is exactly the distinction the first invariant draws.
