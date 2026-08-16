---
issue: two-event-kinds-are-filterable-but-never-emitted
kind: audit
category: vestigial
artifacts:
  - concept:event-log
  - decision:event-log-kind-enum
  - decision:event-log-payload-shapes
status: verified
opened: 2026-08-16T09:40:04Z
---

# Two event kinds accept a filter and are emitted nowhere

The event log's kind vocabulary is a closed generated enum, and the read route validates a kind filter against it. Two kinds — claim-acquired and claim-held — are in the enum, have payload messages and constructor helpers, and are emitted by nothing: zero call sites, zero payload constructions. A filter on either is accepted and returns nothing, indistinguishable from "this never happened here". Two decisions list claim-acquired among the settled typed kinds; nothing anticipates a declared-but-unemitted kind. The ruling decides retire or wire.

## Options

- Retire both kinds and payloads and drop them from the payload-shapes decision; cost: none — no story or consumer reads them.
- Wire real emissions at claim acquire and hold; cost: two new emit sites and a decision on which transitions produce each.
- Add an invariant that every filterable kind has at least one writer, checked by an emit-site enumeration; cost: pairs with either — does not settle these two alone.

The ruling decides whether these kinds are debt or a gap.

## Ruling

> Recommended ruling (/verify-issues): Retire the two kinds and add the writer invariant with its check — a filterable kind nobody emits is a trap by construction, and the check keeps the next one out.
>
> Rationale: the claim family already carries acquisition and resolution kinds that are emitted; two more with no writer add vocabulary without information, and the pre-v1 rule favours removal over carrying. Flip case: if a lineage or dashboard consumer wants the moment a claim is acquired as a distinct row (not the sub-claim or resolution rows), wire them and say which transition writes each.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
