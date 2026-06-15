---
story: named-lock-metric
status: as-is
---

# Operator graphs named-lock acquisitions

## Role

As an operator, I can see named-lock acquisitions in the platform metrics — alongside producer-claim acquisitions — so lock saturation is something I can graph and alert on rather than reconstruct from events.

## Capability

The named-lock acquisition path increments the acquisition metric family, labeled to distinguish named locks from producer claims, following the existing metric naming convention (see `decision:named-lock-metric`).

## Business value

Lock saturation is an operational condition: with named-lock activity on the metrics endpoint, operators graph and alert on it instead of reconstructing it forensically from the event ledger.

## Acceptance

Acquiring a named lock increments an acquisition metric distinguishable from producer-claim acquisitions; an operator watching the metrics endpoint sees named-lock activity move under load.

## Falsifier

Named-lock acquisitions that move no metric — the events ledger is the only trace.

## Proof

Executable proof — a test acquires named locks and asserts the counter's movement and labeling.
