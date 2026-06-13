---
decision: merge-validator-warnings
status: as-is
---

# Validator warnings reach the responses

## Choice

Both the template register handler and the validate endpoint merge the static validator's warnings into the responses' `validation_warnings`; `warnings_as_errors=true` trips on them (see `story:validation-warnings-surfaced`).

## Rationale

The field, the warnings, and the flag all exist; the decision merely connects them.
