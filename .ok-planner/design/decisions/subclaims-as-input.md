---
decision: subclaims-as-input
status: as-is
---

# The dispatch primitive accepts acquired sub-claims

## Choice

Sub-claim acquisition is owned by the claim-tree machinery (see `concept:claim-tree`); the dispatch-children primitive consumes already-acquired sub-claims as input rather than calling the producer's partition-split itself.

## Rationale

Keeps acquisition concerns in `concept:claim-tree` and run-side dispatch concerns in the dispatch primitive; the two surfaces compose without coupling.
