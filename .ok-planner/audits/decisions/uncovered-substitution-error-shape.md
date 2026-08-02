---
audit: uncovered-substitution-error-shape
artifact: decision:uncovered-substitution-error-shape
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:40Z
---

# Uncovered-substitution registration rejection carries the claimed structured envelope entry

Supported. `validateSubstitutionRefCoverage` in `lib/graph/node/template_validator.go` appends a `substitution_ref_uncovered`-kind entry to `ValidationResult.StructuredErrors` for both attribute-substitution refs and message refs; each entry carries `receiver_node_type`/`ref`/`attribute_property` identifying the offending directive and its site, a `suggested_subscribes_entry` that is a flat three-key drop-in object (`node`, `type`, `force_upstream_refresh: false` — the conservative default), and a separate sibling string `suggested_subscribes_note` explaining the flag's re-evaluation implication, matching the decision's split between a copy-pasteable object and a one-sentence note kept out of it. `handleRegisterTemplate` in `lib/control/controlapi/templates.go` folds `res.StructuredErrors` into the same `validation_errors` array returned on registration rejection. `lib/graph/node/template_validator_substitution_coverage_test.go` exercises this end-to-end against `ValidateTemplate`, asserting the entry's kind, that `suggested_subscribes_entry` is exactly the flat 3-key object with no embedded note field, and that `suggested_subscribes_note` is a top-level string sibling.
