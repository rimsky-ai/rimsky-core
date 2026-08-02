---
audit: lineage-exploration
artifact: story:lineage-exploration
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:57Z
---

# Operator walks lineage forward/backward, queries by claim handle, pivots by source/producer

Supported. `lib/control/controlapi/lineage.go` registers nine `lineage:read`-gated GET routes covering every clause: `/lineage/runs/{run_id}` plus `/ancestors` and `/descendants` (`walkLineageRuns`, direction-aware BFS over `substitution_refs` and leaf-run source references), `/lineage/claims/{claim_handle_id}` plus `/ancestors` and `/descendants` (`walkLineageClaims`, BFS over parent/sub claim-handle chains), `/lineage/by-source/{source_type}/{source_id}` (pivot through source), and `/lineage/by-producer/{executor_name}` (pivot through named producer, optionally by version). All nine handlers are exercised in `lib/control/controlapi/lineage_test.go` (18 `Test*` functions checked, including chain-walk, depth-cap, pagination-truncation, and non-run-source-exclusion cases) and again end-to-end in `test/scenarios/lineage_exploration_e2e_test.go` and `test/scenarios/asset_management/forward_lineage_test.go`.
