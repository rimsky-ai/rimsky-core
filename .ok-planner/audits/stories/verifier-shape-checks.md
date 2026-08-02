---
audit: verifier-shape-checks
artifact: story:verifier-shape-checks
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# Template author validates tabular data shape with the bundled verifier-shape-checks executor

Supported. `lib/services/executors/verifier-shape-checks/checks/checks.go::KnownKinds` declares 8 built-in check kinds (`no_nulls`, `nullable_fields_present`, `pk_unique`, `row_count_ratio`, `row_count_absolute`, `value_in_set`, `regex_match`, `numeric_range`), each dispatched by `Run` and driven purely from node-config-declared `attributes.checks` entries (kind/config/severity) against `attributes.rows`, with no custom-verifier code needed. All 8 kinds are exercised in `lib/services/executors/verifier-shape-checks/checks/checks_test.go` for both pass and fail paths plus config-error handling, and `lib/services/test/scenarios/verifier_severity_partition_e2e_test.go` drives `numeric_range` and `no_nulls` cross-stack through a real deployed template and dispatch to a terminal state.
