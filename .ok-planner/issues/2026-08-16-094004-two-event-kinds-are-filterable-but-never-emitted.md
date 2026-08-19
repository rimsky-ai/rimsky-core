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

Finish the feature. This is an underimplemented capability a user wants, not vocabulary debt. `story:event-log-read` promises node lifecycle transitions, message activity, and supervisor decisions in one feed filterable by kind. `concept:event-log` names node transitions among the rows it writes. Every kind the enum declares and the filter accepts gets a writer at the transition it names. That covers the two kinds named here and the six others nothing writes: `state_transition`, `work_rejected`, `no_op_commit`, `claim_resolved`, `attributes_committed`, `message_sent`. Expand the event-kind vocabulary where an observable transition has no kind. Keep the writer invariant and its check, so a filterable kind nobody emits cannot recur.
