---
decision: subclaims-as-input
status: as-is
---

# The dispatch primitive accepts acquired sub-claims

## Choice

The dispatch-children primitive accepts already-acquired sub-claims as input; it does not call the producer's partition-split itself, and the claim-tree machinery (sub-claim acquisition and relatives, see `concept:claim-tree`) is unchanged by this decision.

## Rationale

Preserves the existing factoring; the unification's win is run-side.
