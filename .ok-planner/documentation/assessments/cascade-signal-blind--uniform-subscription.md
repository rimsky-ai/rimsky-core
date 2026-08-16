---
assessment: cascade-signal-blind--uniform-subscription
subject: story:cascade-signal-blind
way: uniform-subscription
release: d977250c
outcome: held
warrant: experiment:cascade-signal-blind
---
# One subscription form reaches every signal an upstream can fire

The signal taxonomy carries exactly three cascade-firing kinds — success terminals, error-class terminals, and per-key attribute-changed signals — and the audit checked all three, with the transient family being audit-only and non-firing. One template on `catalog:images/rimsky-all-in-one` emitted all three, and four receivers each declared a single subscription entry: an exact success terminal, an attribute-changed path, an error wildcard, and the same wildcard under a predicate. All four receivers dispatched exactly once. Every entry used the same declaration keys plus the optional predicate key, so no signal kind needed a special form and an author writing "react to X" does not have to know which kind X belongs to. Seven checks ran and none failed.

## Unverified remainder

None: the passing run demonstrates the way as promised across all three cascade-firing signal kinds.
