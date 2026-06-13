---
story: validation-warnings-surfaced
status: as-is
---

# Template author sees validator advisories in responses

## Role

As a template author, I can see the static validator's advisory warnings in the registration and validation responses — and promote them to errors with the existing flag — so advice the validator already computes reaches me.

## Capability

Both the register handler and the validate endpoint merge the static validator's warnings into the responses' `validation_warnings`, and `warnings_as_errors=true` trips on them (see `decision:merge-validator-warnings`).

## Business value

Advice the validator already computes reaches the author who can act on it — and authors who want a strict gate can promote the same advisories to rejections with the existing flag.

## Acceptance

Registering or validating a template that trips a static-validator advisory (e.g. claims acquired with no acquisition-failure policy declared) returns the advisory in the response's `validation_warnings`; with `warnings_as_errors=true` the same advisory rejects the registration.

## Falsifier

A static-validator warning that is computed but absent from both responses; or `warnings_as_errors=true` not tripping on it.

## Proof

Executable proof — register a template that trips the acquisition-policy advisory and assert it appears in `validation_warnings`; repeat with `warnings_as_errors=true` and assert rejection.
