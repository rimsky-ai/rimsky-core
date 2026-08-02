---
audit: validator-learns-producer-classes
artifact: decision:validator-learns-producer-classes
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# error_types keys range-check against the full declared-vocabulary union, downgraded to a warning on miss

Supported. `validateErrorTypes` (`lib/graph/node/template_validator.go`, annotated `@decision: validator-learns-producer-classes`) builds the union the decision names: the effective executor's declared classes (`hooks.ExecutorDeclaredErrorClasses`), the reserved `acquire/*` prefix plus the 6-member `spec.RuntimeSynthesizedErrorClasses` list (`isRuntimeSynthesizedErrorClass`, covering `template_resolution_failed`, `template_validation_failed`, `executor_schema_unavailable`, `attributes_schema_failed`, `unresolved_executor`, `executor_sync_timeout`), and the declared classes of every claim producer the node's `claim_producers:` block names (enumerated via `RequiredClaimProducers`, which dedupes and walks the full list, not just the first entry). A key matching none of these becomes a `ValidationWarning` (`res.Warnings`), never a `ValidationError` — the function only raises a hard error for an unrecognized `action` value, not for an unrecognized class. This is exercised end-to-end by `test/scenarios/producer_class_routing_test.go`, which asserts registration produces no warning when the key matches the producer's declared class or the acquire/* fallback, and by `lib/graph/node/template_validator_error_types_test.go`.
