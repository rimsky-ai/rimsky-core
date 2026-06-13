---
decision: harness-first-ordering
status: as-is
---

# Harness hardening precedes consolidation refactors

## Choice

When a stabilization campaign mixes test-harness hardening (race gates, deterministic race-injection hooks, polling audits) with consolidation refactors of concurrency seams (the claim spine, child execution), the harness work lands first, and the largest-blast-radius consolidation lands last among the consolidations.

## Rationale

Consolidations refactor concurrency seams; doing so against a race-blind harness is how stabilization passes produce new races. Largest blast radius goes last, behind the most net.
