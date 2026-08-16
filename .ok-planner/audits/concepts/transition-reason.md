---
audit: transition-reason
artifact: concept:transition-reason
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:36:14Z
checked: 7
unaccounted: 5
---

# Whether every node-run state transition carries a reason and passes the next-state validation switch

Unsupported, on one invariant of three. The other two hold cleanly. The reason type is a one-field struct over a free-form kind string with eighteen named values declared beside it, so a caller can construct an unknown kind and the next-state function answers it with the illegal-transition sentinel — the runtime guard the concept describes, not a type-system enum. The forced-teardown reason behaves exactly as claimed: it is accepted from all five in-flight states and drives each to failed, and because the switch has no arm for the two terminal states at all, any reason from fresh or failed falls through to the sentinel; the teardown path emits one settling-signal audit row per killed run and the control surface writes one administrative terminated row for the teardown as a whole, and the reason kind itself reaches no audit row, since event kinds are drawn from a closed generated operational-kind set and signal type-paths, a vocabulary a reason value cannot enter. What fails is the universal that no transition bypasses the validation switch. Enumerating from reality every method in either driver that updates the state column of an existing node-run row — seven, identically in both — only two consult the next-state function: the general state update, which validates the caller's reason against the current state, and the pending-to-stale gate transition, which derives its target from the switch. The other five write a state with no reason at all. Three of them are the release-claim family, which drive a run from running, held, or parked back to stale — a transition no arm of the switch produces, so it is not merely unvalidated but unmodelled — and their callers are ordinary production paths: the frame engine's orphan release, the error-policy retry, the conductor's stale recovery, and the acquire post-commit. Row-creating inserts are excluded from the population as creations rather than transitions.

## Unaccounted

- queue accessor `PromoteClaimedToRunning` — writes the running state guarded only by a stale predicate in the WHERE clause; no reason, no switch
- queue accessor `ReleaseClaim` — writes the stale state from any non-terminal state; running-to-stale is not modelled by the switch
- queue accessor `ForceReleaseClaim` — same write with the claimant guard dropped
- queue accessor `ReleaseClaimWithDisposition` — same write plus prior-dispatch stamping
- run-tree accessor `UpdateStateAndOutcome` — writes a caller-chosen target state and settling signal; two of its three production callers consult the parent next-state function first, but it returns the aggregate sentinel and the written target is the caller's, not the switch's
