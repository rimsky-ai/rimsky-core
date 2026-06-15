---
story: validation-names-the-mode
status: as-is
---

# Operator learns the reference-validation mode from the rejection

## Role

As an operator registering a template whose service references cannot be validated, I am told which reference-validation mode rejected it and which config key changes the behavior, so that the register-before-provision workflow is discoverable from the error message itself.

## Capability

Reference-validation failure messages name the active validation mode, state that the mode is what made the failure fatal, and name the reference-validation mode config key with its relaxed settings (see `decision:validation-error-names-mode`).

## Business value

The validation mode exists precisely for the register-before-provision workflow; an error that names the knob makes the workflow discoverable from the rejection itself instead of requiring out-of-band knowledge.

## Acceptance

The operator registers a template referencing a not-yet-provisioned service under the strict default mode → the rejection states that reference validation failed, names the active mode, says that mode is what made the unprovisioned reference fatal, and names the reference-validation-mode config key (with the relaxed settings) for register-first workflows.

## Falsifier

A reference-validation rejection that reads as a generic "validation rejected the registration" — mode unnamed, config key unnamed.

## Proof

Executable proof — a test registers a template with an unprovisioned reference under strict mode and asserts the rejection body names the active mode and the config key; a companion assertion registers the same template under the relaxed mode and succeeds, proving the advice the error gives is true.
