---
issue: wait-set-insertion-path-not-single
kind: audit
category: conflicting
artifacts:
  - concept:wait-set
status: verified
opened: 2026-08-16T09:05:06Z
---

# The wait-set concept claims a single insertion path; there are three

A wait-set row records that a receiver's queued run is waiting on a sender's in-flight run. The concept says there is a single insertion path, on the cascade walk. There are three: the ordinary subscriber cascade walk, the force-upstream-refresh pull it triggers (both keyed on the receiver's latest cascade-driven pending row), and sub-graph entry-alias binding at child dispatch — which inserts a row keyed on the just-created delegated child's run and marks it drained in the same transaction. The ruling enumerates the sites; routing the third through the cascade-walk helper would be a redesign of delegation's entry wiring, not a text fix.

## Options

- Enumerate the three insertion sites in the concept's Owns section; cost: none.
- Route sub-graph insertion through the cascade-walk helper so there is one path; cost: a delegation redesign for a sentence.

The ruling corrects the count.

## Ruling

> Generated ruling (/verify-issues): Rewrite the concept's "single insertion path" as the three sites it has — the subscriber cascade walk, the force-upstream-refresh pull, and sub-graph entry-alias binding at child dispatch — noting the third is keyed on the delegated child's run and drained in the same transaction. Forced by the current-state-only rule; the third site is by design. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
