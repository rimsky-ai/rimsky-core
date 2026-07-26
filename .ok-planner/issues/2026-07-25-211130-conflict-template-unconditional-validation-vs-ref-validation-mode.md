---
issue: conflict-template-unconditional-validation-vs-ref-validation-mode
kind: audit
category: conflicting
artifacts:
  - concept:template
  - story:ref-validation-mode
  - decision:validation-error-names-mode
status: verified
opened: 2026-07-25T21:11:30Z
---

# Three artifacts describe a registration-validation mode knob the code deliberately deleted

When a workflow template is registered, rimsky checks every service name it references — executors (the services that run work), claim-producer stores, named locks — and rejects the registration if anything is missing. Two stories and a decision describe an operator-selectable strictness knob for that check (`all` / `available` / `none`), built so templates could register before all their services were provisioned. That knob no longer exists: the config key that selected it is on the retired-keys list, and a test asserts the loader rejects it outright with no redirect (`code:lib/control/config/retired_aliases_test.go`). No mode type or mode string survives anywhere in the library code.

The current template concept states the current truth — validation is unconditional, with no operator setting that relaxes it and no register-before-provision path — so the corpus now contains both the current rule and a detailed description of its deleted predecessor. A fourth artifact, the instance concept, still hedges its instantiation gate with "whatever a relaxed registration mode skipped is enforced here," presupposing the deleted knob.

Nothing here is a code question: the deletion was deliberate under the pre-v1 break-freely rule, and the register-before-provision use case is now served by the template-level late-bind list instead.

## Options

- Retire `story:ref-validation-mode`, `story:validation-names-the-mode`, and `decision:validation-error-names-mode`, and strike the "relaxed registration mode" clause from `concept:instance` — the corpus follows the code's deliberate deletion. Cost: none beyond the sprint work; the use case is covered by late-bind.
- Revive the mode in code — a product decision to re-add a deleted feature, which nothing currently motivates.

## Ruling

> Generated ruling (/verify-issues): retire `story:ref-validation-mode`,
> `story:validation-names-the-mode`, and `decision:validation-error-names-mode`,
> and amend `concept:instance` to drop the "relaxed registration mode" clause.
> The code's deliberate deletion of the mode, together with the current-state-only
> rule, forces the corpus to stop describing it.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
