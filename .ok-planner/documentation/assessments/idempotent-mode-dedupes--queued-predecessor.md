---
assessment: idempotent-mode-dedupes--queued-predecessor
subject: story:idempotent-mode-dedupes
way: queued-predecessor
release: d977250c
outcome: held
warrant: experiment:idempotent-mode-dedupes
---
# Opting a node into comparison against its queued predecessor

A sender cascading to itself four times drove a receiver once per round, and the count of the receiver's `catalog:event-kinds/work_started` events is the number of times its executor was actually reached. Under the non-idempotent control all four rounds reached the executor carrying a byte-identical input bag. With `catalog:template-keys/nodes[].cascade_mode` set to compare against the queued predecessor, the same four rounds produced exactly one dispatch — the three re-runs with identical inputs never reached the executor. Re-run with a receiver whose inputs change every round, the same mode dispatched all four rounds with four distinct bags, so the drop follows from input equality and not from rounds being coalesced. An expensive executor is therefore never handed work it has already been handed. Eight checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
