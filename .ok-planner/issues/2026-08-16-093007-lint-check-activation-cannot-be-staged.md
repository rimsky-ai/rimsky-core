---
issue: lint-check-activation-cannot-be-staged
kind: audit
category: conflicting
artifacts:
  - decision:config-flip
status: verified
opened: 2026-08-16T09:30:07Z
---

# The config-flip decision stages a lint-check activation the tool can no longer represent

The lint tool cannot represent the staged activation the config-flip decision describes. The decision sequences how a lint check goes live: the tool ships it inactive, the tree is swept clean, then a configuration flip activates it. The lint's configuration carries no activation map. Both shipped checks run unconditionally. The lint's gating test fails any configuration that marks a check inactive. No check waits to be activated. The ruling decides whether the decision is retired or kept as a rule for a future third check.

## Options

- Retire the decision, recording that the tool can no longer represent activation staging and that the clean-and-active state is asserted directly; cost: loses the documented rationale for why every check is active.
- Keep it as a standing rule for a future check, adding that staging one first requires restoring an activation surface the tool forbids; cost: prose describing a mechanism nothing supports.

The ruling decides whether a rule with no subject stays.

## Ruling

> Recommended ruling (/verify-issues): Retire it. The corpus records current commitments, and a staged activation is not one. If a third check ever needs staging, the decision that adds the activation surface can carry the sequence.
>
> Rationale: design docs are current-state only, and a rule about a mechanism the tool refuses is a roadmap note, not a decision. Flip case: a concrete third check on the way would make the second option the honest text.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
