---
decision: harness-first-ordering
status: as-is
---

# Harness hardening precedes consolidation refactors

## Choice

A stabilization campaign that mixes test-harness hardening (race gates, deterministic race-injection hooks, polling audits) with consolidation refactors of concurrency seams (the claim spine, child execution) is sequenced harness-first: harness hardening precedes any consolidation, and the largest-blast-radius consolidation is the last consolidation in the sequence.

## Rationale

Consolidations refactor concurrency seams; doing so against a race-blind harness is how stabilization passes produce new races. Largest blast radius goes last, behind the most net.
