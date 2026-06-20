---
concept: data-processing
status: as-is
aliases: []
---

# Data processing

## Definition

Optional mix-in protocol on a claim producer. Advertised in the capabilities handshake by listing the data-processing protocol alongside the claim-producer protocol. The protocol carries control-plane operations for the typed-data version lifecycle: capability advertisement, candidate lifecycle (begin / commit / abandon), and read-side surfaces for version history, partition manifests, and per-version schema lookup.

Data motion stays substrate-direct via the acquired result's address; the protocol carries control-plane only.

## Boundaries

Owns: the protocol's operation surface, the producer-candidate-handle lifecycle on sub-claim rows, the parent-run terminal flow's parent-claim commit aggregation step. Does NOT own: the substrate (producer-internal; rimsky doesn't interpret it), the aggregator vocabulary (producer-internal; rimsky doesn't interpret), the asset presentation surface (see `concept:asset`). Adjacent: `concept:claim-producer`, `concept:asset`, `concept:fan-out`, `concept:validation`.

## Invariants

- Begin-candidate is idempotent on the claim-handle plus idempotency-key pair: a retried call returns the existing candidate handle.
- For fan-out with the data-processing protocol: the supervisor calls begin-candidate at sub-claim acquisition time (in the same transaction that inserts the sub-claim's handle row) and persists the opaque candidate-handle bytes onto the sub-claim row. Passed to the leaf executor's execution request.
- Commit-candidate runs at the corresponding leaf-run terminal (success path); abandon-candidate runs on failure / strict-cancel / backfill-abort.
- Parent-run terminal flow: aggregation policy decides "promote" or "abandon"; on promote, the producer's commit verb on the parent claim triggers the producer to aggregate the registered candidates per the aggregator declared on the claim, atomically promote to a canonical version, and return the version identifier. Rimsky records the version identifier on the claim handle and in the lineage ledger.
- The producer's data-block declaration is opaque to rimsky (parsed via the validation protocol at registration; consulted at runtime per producer state).
