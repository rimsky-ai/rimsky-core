---
issue: node-run-state-writes-bypass-transition-switch
kind: audit
category: conflicting
artifacts:
  - concept:transition-reason
status: verified
opened: 2026-08-16T09:04:57Z
---

# Five of the seven node-run state writers bypass the transition-validation switch

The transition-reason concept says every node-run state change goes through one validation switch that pairs a reason with a legal transition, so illegal sequences (double-execute among them) are rejected structurally. Seven methods per backend write the state column; two consult the switch. The other five write a state guarded only by their own where-clauses — and three of them (the release-claim variants) drive running, held or parked back to stale, a transition the switch has no arm for at all, so it is not merely unvalidated but unmodelled. The ruling decides whether the switch becomes universal or the concept names what it governs.

## Options

- Route all five writers through the switch, adding an arm for release-back-to-stale and real reasons for promote-to-running and the run-tree write; cost: a state-machine modelling addition, the highest assurance.
- Amend the invariant to name which write classes the switch governs and which rely on ownership guards; cost: honesty without assurance.
- Narrow the concept to settlement transitions and give queue/claim lifecycle writes their own account; cost: a scope split.

The ruling decides whether "no transition bypasses the switch" is a property or a hope.

## Ruling

> Recommended ruling (/verify-issues): Make it a property — model release-back-to-stale as a transition with its reason, give promote-to-running and the run-tree write theirs, and route all seven writers through the switch.
>
> Rationale: the concept's whole value is that the machine rejects illegal sequences by construction; five unrouted writers, three of them making unmodelled moves, is where the next ordering bug lives, and this project pins state machines rather than documenting around them. Flip case: if the release-claim moves are proven safe by their ownership predicates alone and the owner wants the switch to stay a settlement-only instrument, the third option is the honest scope.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
