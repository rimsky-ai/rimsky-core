---
audit: validation-warnings-surfaced
artifact: story:validation-warnings-surfaced
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# Template author sees validator advisories in register/validate responses, promotable via warnings_as_errors

Supported. `lib/control/controlapi/templates.go::handleRegisterTemplate` merges static-validator warnings with `RunValidationPipeline`'s findings into a `validation_warnings` array returned on both a normal (accepted) response and a rejection, and rejects outright when `warnings_as_errors=true` and any warning is present. `test/scenarios/validation_warnings_test.go::TestValidationWarnings_StaticAdvisorySurfacedAndPromotable` proves the full contract end-to-end against a live control API: a template tripping only the static acquire/unavailable advisory registers successfully (201) with the advisory present in `validation_warnings`, the same advisory appears on `POST /v1/templates/validate`'s response with `ok:true`, `?warnings_as_errors=true` on registration flips the same advisory into a 400 rejection that echoes `warnings_as_errors:true` and persists no template row, and `?warnings_as_errors=true` on `/validate` flips `ok` to `false`.
