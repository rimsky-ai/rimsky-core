---
decision: validation-errors-additive-not-uniform
status: as-is
---

# Validation-error envelopes admit two coexisting entry shapes

## Choice

Validation-error responses are an array of entries that may be of two shapes: a `{path, msg}` entry for simple field violations, and a structured entry carrying a `kind` discriminator plus additional fields for violations that the operator (or an LLM agent) needs to act on programmatically. Both shapes coexist in the same array; consumers distinguish them by the presence of the `kind` discriminator on the richer shape. Substitution-coverage rejections use the structured shape (kind `substitution_ref_uncovered`); missing-flag rejections use the `{path, msg}` shape because their fix is mechanically obvious without a structured suggestion.

## Rationale

Pairing the two shapes lets each kind of violation carry exactly the information its consumer needs — a `{path, msg}` entry is enough when the fix is obvious from the path alone, while a structured entry lets a programmatic consumer apply a copy-pasteable correction without parsing prose.
