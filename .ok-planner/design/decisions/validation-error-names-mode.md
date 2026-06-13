---
decision: validation-error-names-mode
status: as-is
---

# Reference-validation rejections are self-documenting

## Choice

Reference-validation failure messages name the active validation mode, state that the mode made the failure fatal, and name the `templates.ref_validation_mode` config key with its relaxed settings (see `story:validation-names-the-mode`).

## Rationale

The mode exists precisely for the register-before-provision workflow; an error that hides the knob defeats the feature.
