---
audit: validation-errors-additive-not-uniform
artifact: decision:validation-errors-additive-not-uniform
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# Validation-error envelopes admit both a {path, msg} shape and a kind-discriminated structured shape

Supported. `lib/graph/node/template_validator.go::ValidationResult` carries both `Errors []ValidationError` (the `{path, msg}` shape) and `StructuredErrors []map[string]any` (the `kind`-discriminated shape), and `lib/control/controlapi/templates.go::handleRegisterTemplate` appends both into the same `validation_errors` response array. The structured shape is used for uncovered-substitution rejections (`validateSubstitutionRefCoverage` emits `kind: "substitution_ref_uncovered"` entries with a copy-pasteable `suggested_subscribes_entry` and a sibling `suggested_subscribes_note`), proven by `test/scenarios/registration_rejects_uncovered_substitution_test.go` asserting the `kind` discriminator, the 3-key drop-in suggestion object, and the note. The plain shape is used for the missing-flag case named in the decision — `validateSubscribes` rejects a subscription lacking `force_upstream_refresh` with a bare `{path: "...force_upstream_refresh", msg: "...required..."}` entry and no structured suggestion, matching the decision's rationale that a mechanically obvious fix needs no structured envelope.
