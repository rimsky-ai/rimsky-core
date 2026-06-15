---
decision: merge-validator-warnings
status: as-is
---

# Validator warnings reach the responses

## Choice

Both the template-register surface and the validate surface merge the static validator's warnings into their responses' warnings list; the warnings-as-errors mode trips on them (see `story:validation-warnings-surfaced`, `concept:validation`).

## Rationale

The response field, the warnings, and the warnings-as-errors mode all exist; the decision merely connects them.
