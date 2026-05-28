---
concept: data-processing
status: as-is
aliases: []
---

# Data processing

## Definition

Optional mix-in protocol on a claim producer. Advertised in the capabilities handshake by listing the data-processing protocol alongside the claim-producer protocol. Seven control-plane operations for the typed-data version lifecycle:

- **Advertise capabilities** — report the producer's supported data shapes, materializations, partition kinds, and aggregators.
- **Begin a candidate** — open a write context for one work unit, keyed by the holding claim handle and a sub-scope descriptor; idempotent on the claim-handle plus idempotency-key pair, returning an opaque candidate handle.
- **Commit a candidate** — finalize one candidate write, returning the candidate's metadata.
- **Abandon a candidate** — discard a candidate.
- **List versions** — return a claim's asset version history.
- **List partitions** — return the partition manifest for one version.
- **Get a version's schema** — schema lookup for implementer-side adapters.

Data motion stays substrate-direct via the acquired result's address; the protocol carries control-plane only.

## Boundaries

Owns: the seven RPCs above, the producer-candidate-handle lifecycle on sub-claim rows, the parent-run terminal flow's parent-claim commit aggregation step. Does NOT own: the substrate (Parquet, GeoParquet, PostGIS, Iceberg — producer-internal), the aggregator vocabulary (producer-internal; rimsky doesn't interpret), the asset presentation surface (see `concept:asset`). Adjacent: `concept:claim-producer`, `concept:asset`, `concept:fan-out`, `concept:validation`.

## Invariants

- Begin-candidate is idempotent on the claim-handle plus idempotency-key pair: a retried call returns the existing candidate handle.
- For fan-out with the data-processing protocol: the supervisor calls begin-candidate at sub-claim acquisition time (in the same transaction that inserts the sub-claim's handle row) and persists the opaque candidate-handle bytes to the sub-claim row's candidate-handle field. Passed to the leaf executor's execution request.
- Commit-candidate runs at the corresponding leaf-run terminal (success path); abandon-candidate runs on failure / strict-cancel / backfill-abort.
- Parent-run terminal flow: aggregation policy decides "promote" or "abandon"; on promote, the producer's commit verb on the parent claim triggers the producer to aggregate the registered candidates per the aggregator declared in the claim's `data:` block, atomically promote to a canonical version, and return the version identifier. Rimsky records the version identifier on the claim handle and in the lineage ledger.
- The producer's `data:` block is opaque to rimsky (parsed via the validation protocol at registration; consulted at runtime per producer state).

## Notes

Introduced by `spec:2026-05-15-data-platform-extensions-design`. The "control-plane only" rule (data motion via the acquired result's address) is what keeps rimsky substrate-agnostic. The H-cut block of the plan removes the bundled reference data stores (the parquet, geo-parquet, and geo-postgis stores); the stub store's data-processing extension is the self-test target until consumer-side stores ship.

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
