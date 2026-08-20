---
issue: node-run-state-writes-bypass-transition-switch
kind: audit
category: conflicting
artifacts:
  - concept:transition-reason
status: promoted
opened: 2026-08-16T09:04:57Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# Five of the seven node-run state writers bypass the transition-validation switch

The transition-reason concept says one validation switch guards every node-run state change. The switch pairs a reason with a legal transition, so it rejects illegal sequences structurally, including double-execute. Each backend has seven methods that write the state column, and two of them consult the switch. The other five write a state guarded only by their own where-clauses. Three of those five are the release-claim variants. They drive running, held or parked back to stale, and the switch has no arm for that transition, so the move is unmodelled rather than merely unvalidated. The ruling decides whether the switch becomes universal or the concept names what it governs.

## Options

- Route all five writers through the switch, adding an arm for release-back-to-stale and real reasons for promote-to-running and the run-tree write; cost: adds to the state-machine model, and gives the highest assurance.
- Amend the invariant to name which write classes the switch governs and which rely on ownership guards; cost: the text becomes honest and nothing gains assurance.
- Narrow the concept to settlement transitions and give queue/claim lifecycle writes their own account; cost: splits the scope.

The ruling decides whether "no transition bypasses the switch" is an enforced property or an unenforced claim.

## Ruling

> Recommended ruling (/verify-issues): Enforce the property. Model release-back-to-stale as a transition with its reason, give promote-to-running and the run-tree write their reasons, and route all seven writers through the switch.
>
> Rationale: the concept is valuable because the machine rejects illegal sequences by construction. Five unrouted writers, three of them making unmodelled moves, are where the next ordering bug will appear. This project pins state machines rather than documenting around them. Flip case: if the release-claim moves are proven safe by their ownership predicates alone, and the owner wants the switch to stay a settlement-only instrument, the third option states the honest scope.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
