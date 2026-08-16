---
audit: parked-state
artifact: concept:parked-state
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:57:08Z
---

# The parked hold state: its signals, its two wake paths, and its six invariants

Unsupported on one invariant clause; everything else holds. Parked is one of the seven run states, entered from running on the park terminal, and it sits in the shared five-value in-flight list the rest of the runtime keys off. The park signal family is audit-only: the subscription validator rejects the park type-path and its wildcard outright with a message saying so, so no template can subscribe to a park settle, and the park payload message declares resume-at, tags, a scratch byte count, and a spilled flag — never the scratch bytes — with the spill predicate returning false at length zero, which is the stated zero-length case. Both wake paths go through one shared resume routine that transitions the row to stale, clears resume-at, and appends the wake event with its reason; neither transitions to running, the row is its own resume row with no copy step, and the cascade wake mutates state only while the round lands on a new pending row, which the cascade walker's own code and its two tests confirm. The orphan dispatch sweep drops parked rows because parking clears the claim, with a conformance case on both backends. The defect is in the force-fail clause: the concept states a parked row is force-failed only by an instance kill. The state machine admits two reasons out of parked into failed — the instance kill and the fan-out sibling cancellation — and the sibling-cancellation walk force-fails every non-terminal run in the cancelled subtree, parked rows included, stamping the sibling-failed settling signal rather than the kill signal. The node-run concept's own transition table lists both reasons for that edge, so the corpus contradicts this clause as well as the code does.
