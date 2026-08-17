---
issue: store-specific-partition-idiom-count-stale
kind: audit
category: conflicting
artifacts:
  - decision:fanout-list-array-store-agnostic
status: promoted
opened: 2026-08-16T08:48:06Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The fan-out decision's rationale says one store-specific partition idiom exists; three do

The decision that keeps fan-out grammars split by semantics rather than by backend argues, in its rationale, that folder expansion is the one genuinely store-specific partition idiom. There are three: folder expansion and batch pick on the filesystem producer, and partition policy on the Postgres producer. Only folder expansion has a story. The Choice is unaffected. The ruling corrects the count.

## Options

- Correct the enumeration to name all three; cost: none.
- Also author stories for batch pick and partition policy so each store-specific grammar has one; cost: two new stories — optional, nothing mandates a story per capability.

The ruling corrects the rationale; the story question is the owner's separately.

## Ruling

> Generated ruling (/verify-issues): Correct the rationale to name the three store-specific partition idioms — folder expansion and batch pick on the filesystem producer, partition policy on the Postgres producer — and drop the "with its own story" clause, which is true of folder expansion only. Forced by the current-state-only rule; the Choice stands. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
