---
audit: verifier-severity-partition
artifact: story:verifier-severity-partition
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# Template author distinguishes warning vs error severity on shape checks

Supported. `lib/services/executors/verifier-shape-checks/server.go::executeCore` parses each declared check's `severity` (`checks.SeverityError` default, or `checks.SeverityWarning`), counts only error-severity failures toward `blockingFailures`, and folds warning-severity failures into `verifier_warnings`/`verifier_warning_count` on the success delta rather than blocking; a blocking failure returns a `verifier/check_failed/<kind>` terminal error. `lib/services/test/scenarios/verifier_severity_partition_e2e_test.go::TestVerifierSeverityPartition` drives both legs cross-stack: a template with a warning-severity `no_nulls` failure alongside a passing error-severity `numeric_range` check settles fresh with `verifier_warning_count >= 1` and a matching warning entry, while the same template with the error-severity `numeric_range` check itself failing settles failed with an event carrying `error_class = verifier/check_failed/numeric_range`, proving the warning leg never blocks and the error leg always does.
