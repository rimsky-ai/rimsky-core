---
decision: subclaims-as-input
status: as-is
---

# The dispatch primitive accepts acquired sub-claims

## Choice

Sub-claim acquisition is owned by fan-out's partition-split mechanics (see `concept:fan-out`); the dispatch-children primitive consumes already-acquired sub-claims as input rather than calling the producer's partition-split itself.

## Rationale

Keeps acquisition concerns in `concept:fan-out` and run-side dispatch concerns in the dispatch primitive; the two surfaces compose without coupling.

## Alternatives

- The dispatch primitive calls the producer's partition-split itself — rejected: entangles run-side dispatch with the acquisition mechanics fan-out owns, coupling the two surfaces.
