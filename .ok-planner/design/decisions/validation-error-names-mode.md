---
decision: validation-error-names-mode
status: as-is
---

# Reference-validation rejections are self-documenting

## Choice

Reference-validation failure messages name the active validation mode, state that the mode made the failure fatal, and name the template reference-validation-mode configuration knob with its relaxed settings (see `story:validation-names-the-mode`, `concept:validation`).

## Rationale

The mode exists precisely for the register-before-provision workflow; an error that hides the knob defeats the feature.
