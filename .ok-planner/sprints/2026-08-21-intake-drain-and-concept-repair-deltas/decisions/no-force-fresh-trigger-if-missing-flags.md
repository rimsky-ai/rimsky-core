---
decision: no-force-fresh-trigger-if-missing-flags
---

# No always-re-execute or lazy-initialization read flags

## Choice

The substitution grammar carries no always-re-execute flag and no lazy-upstream-initialization flag. An author expresses re-execution through explicit invalidation, and lazy upstream initialization through the cascade's proactive upstream pull declared on the receiver's subscription (see `concept:attribute`, `concept:cascade`).

## Rationale

Both flags would put scheduling control on the read surface. A read directive names a value; whether an upstream runs is a cascade question, and the cascade surface already answers it. Two surfaces controlling execution would let a template's declared edge set disagree with what its reads imply, so an operator could no longer learn from the subscriptions what triggers what.

## Alternatives

- Add a per-read always-re-execute flag — rejected: a read then schedules work, so the graph's execution shape stops being visible on the cascade surface.
- Add a per-read lazy-initialization flag — rejected: it duplicates the proactive upstream pull the subscription already declares, and the two can disagree about one edge.
