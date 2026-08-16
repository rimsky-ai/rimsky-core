---
issue: lint-check-activation-cannot-be-staged
kind: audit
category: conflicting
artifacts:
  - decision:config-flip
status: verified
opened: 2026-08-16T09:30:07Z
---

# The config-flip decision sequences a lint-check activation the tool can no longer represent

The config-flip decision describes how a lint check goes live: shipped inactive, the tree swept clean, then activated by a configuration flip. The lint's configuration carries no activation map, both shipped checks run unconditionally, and its gating test fails any configuration that marks a check inactive. The staged transition cannot be represented, and no check is waiting to be activated. The ruling decides whether the decision is retired or kept as a rule for a future third check.

## Options

- Retire the decision, recording that activation staging is no longer representable and the clean-and-active state is asserted directly; cost: loses the documented rationale for why every check is active.
- Keep it as a standing rule for a future check, adding that staging one first requires restoring an activation surface the tool forbids; cost: prose describing a mechanism nothing supports.

The ruling decides whether a rule with no subject stays.

## Ruling

> Recommended ruling (/verify-issues): Retire it — the corpus records current commitments, and a staged activation is not one; if a third check ever needs staging, the decision that adds the activation surface can carry the sequence.
>
> Rationale: design docs are current-state only; a rule about a mechanism the tool refuses is a roadmap note in a decision's clothing. Flip case: a concrete third check on the way would make the second option the honest text.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
