---
concept: data-processing
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Data processing

## Definition

Optional mix-in protocol on `ClaimProducer`. Advertised in `Capabilities` via `protocols: [claim_producer, data_processing]`. Control-plane methods for typed-data version lifecycle:

- **`Capabilities() → {data_shapes, materializations, partition_kinds, aggregators}`**.
- **`BeginCandidate(claim_handle_id, sub_scope_descriptor, idempotency_key) → candidate_handle`** — open a write context for one work unit. Idempotent on `(claim_handle_id, idempotency_key)`.
- **`CommitCandidate(candidate_handle) → candidate_metadata`** — finalize one candidate write.
- **`AbandonCandidate(candidate_handle)`** — discard a candidate.
- **`ListVersions(claim_handle_id) → [version_metadata]`** — asset version history.
- **`ListPartitions(claim_handle_id, version_id) → [partition_descriptor]`** — partition manifest.
- **`GetVersionSchema(claim_handle_id, version_id) → schema_metadata`** — schema lookup for SDK adapters.

Data motion stays substrate-direct via the `ClaimResult.address`; the protocol carries control-plane only.

## Boundaries

Owns: the seven RPCs above, the `producer_candidate_handle` lifecycle on sub-claim rows, the parent-run terminal flow's `Commit(parent_claim_handle_id)` aggregation step. Does NOT own: the substrate (Parquet, GeoParquet, PostGIS, Iceberg — producer-internal), the aggregator vocabulary (producer-internal; rimsky doesn't interpret), the asset presentation surface (see `concept:asset`). Adjacent: `concept:claim-producer`, `concept:asset`, `concept:fan-out`, `concept:validation`.

## Invariants

- `BeginCandidate` is idempotent on `(claim_handle_id, idempotency_key)`: a retried call returns the existing `candidate_handle`.
- For fan-out-with-DataProcessing: the supervisor calls `BeginCandidate` at sub-claim acquisition time (in the same transaction that inserts the sub-claim's `rimsky_claim_handles` row) and persists the opaque `candidate_handle` bytes to the sub-claim row's `producer_candidate_handle` column. Passed to the leaf executor's `ExecuteRequest`.
- `CommitCandidate` runs at the corresponding leaf-run terminal (success path); `AbandonCandidate` runs on failure / strict-cancel / backfill-abort.
- Parent-run terminal flow: aggregation policy decides "promote" or "abandon"; on promote, `ClaimProducer.Commit(parent_claim_handle_id)` triggers the producer to aggregate the registered candidates per the aggregator declared in the claim's `data:` block, atomically promote to a canonical version, and return `version_id`. Rimsky records `version_id` in `rimsky_claim_handles` and `rimsky_lineage`.
- The producer's `data:` block is opaque to rimsky (parsed via `Validation` at registration; consulted at runtime per producer state).

## Annotation sites

- `code:protocols/proto/v1/data_processing.proto` — protobuf surface.
- `code:protocols/proto/v1/gen/data_processing*.pb.go` — generated bindings.
- `code:stores/stub/dataprocessing/` — reference impl (in-memory; self-test target).
- `code:cmd/rimsky-data-processing-conformance/` — conformance suite.
- `code:runtime/runner_subclaim.go` — `BeginCandidate` orchestration at sub-claim acquisition.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The "control-plane only" rule (data motion via `ClaimResult.address`) is what keeps rimsky substrate-agnostic. The H-cut block of the plan removes bundled reference stores (`parquet-store`, `geo-parquet-store`, `geo-postgis-store`); the stub-store's DataProcessing extension is the self-test target until consumer-side stores ship.
