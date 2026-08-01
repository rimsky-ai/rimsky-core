---
story: held-abandon-cascades-abandoned
status: as-is
---

# Downstream hears an abandoned signal when held work rolls back

## Story

As a template author whose downstream node must react when an upstream's held work is rolled back, I can subscribe to the abandoned-error signal — alone or through the broader error-family pattern (`concept:signal`) — and see it fire at the moment the held work is abandoned, so that my downstream compensates for the rollback instead of never learning it happened.
