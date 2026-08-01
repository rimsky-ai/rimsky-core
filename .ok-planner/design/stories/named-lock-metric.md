---
story: named-lock-metric
status: as-is
---

# Operator graphs named-lock acquisitions

## Story

As an operator, I can see named-lock acquisitions in the platform metrics — alongside producer-claim acquisitions — so lock saturation is something I can graph and alert on rather than reconstruct from events.

The named-lock acquisition path increments the acquisition metric family, labeled to distinguish named locks from producer claims, following the existing metric naming convention (see `decision:named-lock-metric`).

Lock saturation is an operational condition: with named-lock activity on the metrics endpoint, operators graph and alert on it instead of reconstructing it forensically from the event ledger.
