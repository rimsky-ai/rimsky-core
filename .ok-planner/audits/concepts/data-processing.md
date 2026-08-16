---
audit: data-processing
artifact: concept:data-processing
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:21:03Z
---

# The data-processing mix-in: candidate lifecycle, fan-out threading, and version recording

Unsupported, on half of one invariant; the other four hold. The mix-in is advertised by listing the protocol alongside the claim-producer protocol in the capabilities handshake and is consulted only through the control-plane registry, with the substrate reached by the acquired result's address rather than through the protocol. Begin-candidate idempotency on the claim-handle-plus-key pair is a producer obligation and the shipped conformance battery checks it directly. The fan-out threading is exactly as stated: the supervisor calls begin-candidate for each sub-scope inside the same transaction that inserts that sub-claim's handle row, keys the call by the dispatching run and the partition key, persists the returned opaque bytes on the row, and the dispatcher hands those same bytes to the leaf executor on its execution request. The candidate terminals are correctly polarised — commit-candidate fires only on a committing outcome, abandon-candidate on every abandoning one including both the strict-sibling and descendant cancel outcomes — because the terminal decision routes on one outcome value that all three abandon kinds share. The data block is opaque: the only two things done with those bytes anywhere are a byte comparison for graph equivalence and forwarding them in the registration-time validation request. The clause that fails is the last half of the parent-resolution invariant. The aggregation policy does decide promote or abandon, the producer's commit verb on the parent does return a version identifier, and rimsky does record it on the claim handle — but only from the deferred commit response, which arrives after the outbox delivers the verb, whereas the lineage record for that same resolution is written earlier in the settlement transaction and reads the version identifier off the handle before it is set. The lineage table has an insert and no update, so no later write corrects it; the version identifier a promotion mints therefore never reaches the lineage ledger, and no test asserts that it does.
