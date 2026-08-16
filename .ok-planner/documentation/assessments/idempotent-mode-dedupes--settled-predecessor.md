---
assessment: idempotent-mode-dedupes--settled-predecessor
subject: story:idempotent-mode-dedupes
way: settled-predecessor
release: d977250c
outcome: held
warrant: experiment:idempotent-mode-dedupes
---
# Opting a node into comparison against the most recent settled run as well

With `catalog:template-keys/nodes[].cascade_mode` set to the mode that also compares against the most recent settled run, the same four cascade rounds with byte-identical inputs produced exactly one dispatch, the three re-runs being dropped before the executor. With inputs that differ each round the same mode dispatched all four, carrying four distinct bags. Both idempotent modes therefore behave the same way on the promise's terms, and a template author picks between them on how far back the comparison should reach rather than on whether the drop happens.

## Unverified remainder

None: the passing run demonstrates the way as promised.
