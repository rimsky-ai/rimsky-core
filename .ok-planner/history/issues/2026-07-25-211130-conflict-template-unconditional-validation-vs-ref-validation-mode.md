---
issue: conflict-template-unconditional-validation-vs-ref-validation-mode
kind: audit
category: conflicting
artifacts:
  - concept:template
  - story:ref-validation-mode
  - decision:validation-error-names-mode
status: promoted
sprint: 2026-08-01-guidance-realignment-drain.md
opened: 2026-07-25T21:11:30Z
---

# Three artifacts describe a registration-validation mode knob the code deliberately deleted

When a workflow template is registered, rimsky checks every service name it references — executors (the services that run work), claim producers, named locks — and rejects the registration if anything is missing. Two stories and a decision still describe an operator-selectable strictness knob for that check (`all` / `available` / `none`), built so templates could register before all their services were provisioned. That knob no longer exists: the config key that selected it is on the retired-keys list, a test asserts the loader rejects it outright with no redirect (`code:lib/control/config/retired_aliases_test.go`), and no mode type or mode string survives anywhere in the library code. The deletion was deliberate under the pre-v1 break-freely posture, and the use case the knob served is covered by the template-level late-bind list (`concept:template`'s register-before-provision invariant).

So the corpus now contains both the current rule — `concept:template` correctly states validation is unconditional — and a detailed description of its deleted predecessor. A fourth artifact, `concept:instance`, still hedges its instantiation gate with "whatever a relaxed registration mode skipped is enforced here," presupposing the deleted knob. The current-state-only rule forces the corpus to stop describing retired machinery; retiring two stories and a decision is an intent-level mutation only a sprint may make, which is the only reason this file still exists.

## Options

- **Retire `story:ref-validation-mode`, `story:validation-names-the-mode`, and `decision:validation-error-names-mode`, and strike the relaxed-mode clause from `concept:instance`** — the corpus follows the code's deliberate deletion; the use case is already covered by late-bind.
- **Revive the mode in code** — a product decision to re-add a deleted feature nothing currently motivates.

The ruling is a formality; the rules force the first option.

## Ruling

> Generated ruling (/verify-issues): retire story:ref-validation-mode,
> story:validation-names-the-mode, and
> decision:validation-error-names-mode, and amend concept:instance to
> drop the "relaxed registration mode" clause. The code's deliberate
> deletion of the mode together with the current-state-only rule
> forces the corpus to stop describing it; only the retirements make
> this sprint work.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
