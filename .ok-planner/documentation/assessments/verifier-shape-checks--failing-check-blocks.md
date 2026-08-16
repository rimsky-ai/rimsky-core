---
assessment: verifier-shape-checks--failing-check-blocks
subject: story:verifier-shape-checks
way: failing-check-blocks
release: d977250c
outcome: held
warrant: experiment:verifier-shape-checks
---
# Data that violates a declared check fails the node, naming the check

Rows violating two of the declared checks blocked instead of settling fresh, and the terminal named the failing check kind under `catalog:error-classes/verifier/check_failed`. The declaration is what governs, not the data alone: the same clean rows re-submitted under a stricter declaration were rejected under the added check. An author therefore tightens or loosens enforcement by editing the declaration and nothing else.

## Unverified remainder

None: the passing run demonstrates the way as promised.
