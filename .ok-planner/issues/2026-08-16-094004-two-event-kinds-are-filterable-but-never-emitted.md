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

# Two event kinds accept a filter and nothing emits them

The event log's kind vocabulary is a closed generated enum, and the read route validates a kind filter against it. Two kinds, claim-acquired and claim-held, sit in the enum and carry payload messages and constructor helpers. Nothing emits them: zero call sites, zero payload constructions. The read route accepts a filter on either kind and returns nothing. A caller cannot tell that empty result apart from "this never happened here". Two decisions list claim-acquired among the settled typed kinds, and neither anticipates a declared kind that nothing emits. The ruling decides whether to retire the two kinds or wire them.

## Options

- Retire both kinds and their payloads and drop them from the payload-shapes decision; cost: none, because no story or consumer reads them.
- Emit both kinds at claim acquire and claim hold; cost: two new emit sites, and a decision naming which transitions produce each.
- Add an invariant that every filterable kind has at least one writer, and check it by enumerating emit sites; cost: this option pairs with either of the others and does not settle these two kinds alone.

The ruling decides whether these kinds are debt or a gap.

## Ruling

> Recommended ruling (/verify-issues): Retire the two kinds and add the writer invariant with its check. A filterable kind nobody emits is a trap by construction, and the check keeps the next one out.
>
> Rationale: the claim family carries acquisition and resolution kinds that the code emits. Two more kinds with no writer add vocabulary without information. The pre-v1 rule favours removal over carrying them forward. Flip case: if a lineage or dashboard consumer wants the moment a claim is acquired as a distinct row, and not the sub-claim or resolution rows, wire the two kinds and say which transition writes each.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
