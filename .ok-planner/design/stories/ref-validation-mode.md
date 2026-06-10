---
story: ref-validation-mode
status: as-is
---

# Operator chooses registration-time strictness

## Role

As an operator setting up a staged bring-up where templates register before all referenced services exist, I can choose a registration-time reference-validation strictness mode — `all` (refuse anything missing), `available` (validate only what's provisioned), `none` (skip altogether) — with whatever a relaxed mode lets through caught at the mandatory instantiation gate, so that infra-as-code bring-up is an explicit operator choice rather than implicit heuristic.

## Capability

Operator-selected registration-time reference-validation strictness (`all` / `available` / `none`); the mandatory instantiation gate catches anything a relaxed mode let through.

## Business value

Operators choose explicitly between strict bring-up safety and staged bring-up flexibility; the always-on instantiation gate ensures relaxed registration never produces a running instance with missing refs.

## Acceptance

With the `all` mode, registering a template whose node references a not-yet-provisioned executor / store / lock is refused with a clear missing-reference error. With the `available` mode, the same registration succeeds while still validating refs to provisioned services. With the `none` mode, registration succeeds with no registration-time ref validation. In every mode, whatever the relaxed strictness let through is caught by the mandatory instantiation gate before the instance runs.

## Falsifier

Any mode's stated behavior isn't realized (strict accepts missing refs, or available rejects a real-reference-to-provisioned-service), OR the implicit always-on soft-fail heuristic is still present alongside the explicit modes.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
