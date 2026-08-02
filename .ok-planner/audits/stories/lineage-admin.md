---
audit: lineage-admin
artifact: story:lineage-admin
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:57Z
---

# Operator prunes lineage records older than a cutoff

Supported. `POST /admin/lineage/prune` (`lib/control/controlapi/lineage.go::handleLineagePrune`, gated on `lineage:prune`) accepts a required RFC3339 `before` cutoff and calls `Persist.Lineage().DeleteOlderThan`, with a dry-run mode reporting the would-be-deleted count via `CountOlderThan`. The persistence-layer methods are implemented for both backends (`lib/foundation/persistence/postgres/lineage.go`, `lib/foundation/persistence/sqlite/lineage.go`) and exercised by the shared persistence conformance suite (`lib/foundation/persistence/conformance/lineage.go`, run against both). The route is covered by `TestLineagePrune_DryRunCountMatchesLiveDelete` in `lib/control/controlapi/lineage_test.go` and by an end-to-end scenario test, `test/scenarios/lineage_admin_prune_e2e_test.go`.
