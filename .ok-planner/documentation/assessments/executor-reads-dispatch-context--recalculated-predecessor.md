---
assessment: executor-reads-dispatch-context--recalculated-predecessor
subject: story:executor-reads-dispatch-context
way: recalculated-predecessor
release: d977250c
outcome: held
warrant: experiment:executor-reads-dispatch-context
---
# Reading that this dispatch is a re-run against recalculated inputs

An agent node subscribed to a fan-out node, so the fan-out's settlement recalculated it and it ran twice. Its first dispatch read no predecessor; its second read a predecessor disposition naming a recalculation and named the first dispatch as that predecessor. Across the run the script observed three distinct dispositions — none, a stale recovery, and a recalculation — from the dispatch context alone, so an agent author can branch on all three without inferring any of them.

## Unverified remainder

None: the passing run demonstrates the way as promised.
