---
issue: intent-carry-verbatim-decision-doc-stale
kind: sprint
category: intent-ledger
artifacts:
  - decision:carry-verbatim-requires-one
  - concept:child-execution
status: verified
opened: 2026-08-01T22:42:00Z
---

# A decision describes a retired aggregation policy — and its validator branch may itself break a ratified rule

Fan-out nodes declare an aggregation policy: how child outcomes combine into the parent's. One historical policy, carry-verbatim (pass the single child's outcome through untouched), was removed from the author-facing vocabulary — the valid set is now four values, which the child-execution and fan-out concepts both state. But the recorded decision about it still describes the old world: carry-verbatim as a conditionally valid policy requiring exactly one child (`decision:carry-verbatim-requires-one`). The code contradicts that: the template validator unconditionally rejects `carry_verbatim` regardless of child count, with a targeted message redirecting authors to the four valid policies (`code:lib/graph/node/template_validator_holds.go`).

Investigation surfaced a second, sharper tension the filer missed. A separate ratified ruling — recorded at transcript tier and matched exactly by the pure-removal decision (`decision:pre-v1-pure-removal-for-retired-surfaces`) — says retired surfaces are erased absolutely: no detection rule, no migration error string, no parser case that names the old shape; generic unknown-value rejection is the only legal failure mode. The validator's named `carry_verbatim` case with its targeted redirect is precisely the pattern that decision rejects. So the stale decision isn't just stale — its remaining code footprint appears non-compliant with a standing commitment.

## Options

- Retire the stale decision and collapse the named validator case into the generic unknown-value branch — the concepts already carry the four-value vocabulary, and the code comes into line with the pure-removal rule; cost: template authors migrating old templates lose the targeted hint.
- Rewrite the decision to record current behavior (unconditional rejection with redirect) — keeps the targeted hint, but then the pure-removal decision needs an explicitly recorded exception, since the two would contradict.
- Retire the decision and leave the code as-is — resolves the staleness while leaving a standing commitment silently violated; the least coherent end state.

The ruling decides which pair — decision text and validator branch — survives.

## Ruling

> Recommended ruling (/verify-issues): retire the stale decision and fold the validator's named carry-verbatim case into the generic unknown-value rejection. The four-value vocabulary already lives in the concepts, a policy with no live alternative is not a decision, and the pure-removal rule the owner ratified says named recognition of retired shapes is exactly what must not exist.
>
> Rationale: the rewrite option preserves a migration hint at the price of carving a recorded exception into a rule the owner made absolute six weeks ago — a bad trade pre-v1, when the rule's whole value is that it has no exceptions; the leave-code option resolves the paperwork while keeping the violation. Flip case: if the owner decides migrating old templates deserves targeted errors as a class, that reverses the pure-removal ruling itself, and this validator case becomes the sanctioned pattern rather than the violation — but that is a reversal to make explicitly, not inherit by inertia.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
