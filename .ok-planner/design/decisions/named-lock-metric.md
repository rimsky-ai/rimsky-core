---
decision: named-lock-metric
status: as-is
---

# Named-lock acquisitions are countable

## Choice

The named-lock acquisition surface increments the acquisition metric family, labeled to distinguish named locks from producer claims, following the existing metric naming convention (see `story:named-lock-metric`, `concept:named-lock`, `concept:observability`).

## Rationale

Lock saturation is an operational condition; events are forensics, metrics are monitoring.
