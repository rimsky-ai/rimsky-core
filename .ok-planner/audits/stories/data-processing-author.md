---
audit: data-processing-author
artifact: story:data-processing-author
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:14Z
---

# Claim-producer author implements the typed-data (data-processing) mix-in protocol

Supported. `lib/protocols/proto/v1/data_processing.proto` defines exactly the surface the story claims: a `Capabilities` RPC (data shapes, materializations, partition kinds, aggregators), the `BeginCandidate` / `CommitCandidate` / `AbandonCandidate` per-partition staging verbs, and the `ListVersions` / `ListPartitions` / `GetVersionSchema` listing verbs, advertised alongside the claim-producer protocol in `CapabilitiesResponse.protocols`. Rimsky-side wiring (`lib/runtime/data_processing.go`, `runner_subclaim.go`, `terminal_decision.go`, `runner_acquire_error_policy.go`) allocates a staging candidate at sub-claim acquisition for fan-out, commits it at leaf success and abandons it at leaf failure/cancel, and `lib/control/controlapi/assets.go` exposes the listing verbs (version history, partitions, schema) through the control API. The full lifecycle is exercised end-to-end: `test/scenarios/leaf_candidate_handle_e2e_test.go` proves each of 3 fan-out partitions gets a distinct, persisted candidate handle; `test/scenarios/asset/concurrent_partition_staging_test.go` and `test/scenarios/asset_management/*` exercise commit/abandon and version listing; and `lib/protocols/conformance/dataprocessing/runner.go` runs a dedicated conformance suite (begin/commit/abandon, list-versions/list-partitions/get-version-schema smoke checks, and an abandon-excluded-from-list-versions check) against any implementation, backed by a reference stub server and a reference author example under `examples/data-processing`.
