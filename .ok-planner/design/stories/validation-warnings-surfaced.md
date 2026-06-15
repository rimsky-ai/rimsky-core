---
story: validation-warnings-surfaced
status: as-is
---

# Template author sees validator advisories in responses

## Role

As a template author, I can see the static validator's advisory warnings in the registration and validation responses — and promote them to errors with the existing flag — so advice the validator already computes reaches me.

## Capability

Both the register handler and the validate endpoint merge the static validator's warnings into the responses' validation-warnings field, and the warnings-as-errors flag trips on them when set (see `decision:merge-validator-warnings`).

## Business value

Advice the validator already computes reaches the author who can act on it — and authors who want a strict gate can promote the same advisories to rejections with the warnings-as-errors flag.

## Acceptance

Registering or validating a template that trips a static-validator advisory (e.g. claims acquired with no acquisition-failure policy declared) returns the advisory in the response's validation-warnings field; with the warnings-as-errors flag set the same advisory rejects the registration.

## Falsifier

A static-validator warning that is computed but absent from both responses; or the warnings-as-errors flag not tripping on it.

## Proof

Executable proof — register a template that trips the acquisition-policy advisory and assert it appears in the response's validation-warnings field; repeat with the warnings-as-errors flag set and assert rejection.
