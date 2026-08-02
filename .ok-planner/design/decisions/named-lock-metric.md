---
decision: named-lock-metric
---

# Named-lock acquisitions are countable

## Choice

The named-lock acquisition surface increments the acquisition metric family, labeled to distinguish named locks from producer claims, following the existing metric naming convention (see `story:named-lock-metric`, `concept:named-lock`, `concept:observability`).

## Rationale

Lock saturation is an operational condition; events are forensics, metrics are monitoring.

## Alternatives

- Record acquisitions as event-log rows instead — rejected: events answer "what happened here" after the fact; saturation monitoring needs a scrapeable counter.
- A new dedicated metric family for named locks — rejected: the existing acquisition family with a distinguishing label keeps one naming convention and lets dashboards compare lock kinds directly.
