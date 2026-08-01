---
story: validation-names-the-mode
status: as-is
---

# Operator learns the reference-validation mode from the rejection

## Story

As an operator registering a template whose service references cannot be validated, I am told which reference-validation mode rejected it and which config key changes the behavior, so that the register-before-provision workflow is discoverable from the error message itself.

Reference-validation failure messages name the active validation mode, state that the mode is what made the failure fatal, and name the reference-validation mode config key with its relaxed settings (see `decision:validation-error-names-mode`).

The validation mode exists precisely for the register-before-provision workflow; an error that names the knob makes the workflow discoverable from the rejection itself instead of requiring out-of-band knowledge.
