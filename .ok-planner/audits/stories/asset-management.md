---
audit: asset-management
artifact: story:asset-management
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:18Z
---

# Operator can list, inspect versions/materialization-history, retire, and trace lineage of instance-produced assets

Supported. `lib/control/controlapi/assets.go` registers all five claimed operator surfaces — `GET /v1/instances/{id}/assets` (list, filtered to committed-durable claims against producers that advertise data-processing), `GET .../assets/{alias}` (detail), `GET .../assets/{alias}/versions` (version history via the producer's data-processing client), `GET .../assets/{alias}/materialization-history` (the lineage-record audit trail), and `DELETE .../assets/{alias}` (retire, refusing when any run still actively holds the claim) — mirrored by CLI subcommands `asset list|show|versions|delete|lineage` in `cmd/rimsky/cli/asset.go`. `test/scenarios/asset_management/asset_management_e2e_test.go` exercises list/detail/versions/materialization-history/delete against a real data-processing-capable producer stub, cross-checks per-asset history rowcount against the persisted lineage table, and confirms cross-instance isolation and message-triggered re-materialization (both empty-message whole-instance and, per `test/scenarios/asset_management/forward_lineage_test.go`, a downstream node genuinely consuming the asset's output is surfaced by `GET /v1/lineage/by-source/run/{id}`, satisfying "trace lineage ... including what downstream work consumed an asset."
