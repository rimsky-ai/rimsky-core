---
audit: executor-reads-dispatch-context
artifact: story:executor-reads-dispatch-context
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# An agent script reads its dispatch identity and disposition at runtime

Supported. An agent script driving a bundled claude-agent node read its own
dispatch identity and disposition on every dispatch of one run and wrote what it
read into the node's output attributes. A plain dispatch reported its own
dispatch id, matching the id rimsky recorded for that run, a non-empty run-scope
id, and no predecessor. A node whose first dispatch blocked without reporting
was reaped under `max_quiet_period` and re-dispatched: the second dispatch
reported the first as its predecessor with disposition `stale_recovery`, and the
script reached success only on the branch it takes when a predecessor is
present. A node subscribed to a fan-out sender ran twice: the first dispatch
reported no predecessor, the second reported disposition `recalculate` naming
the first. The disposition vocabulary has three members; two of them were
produced in this run alongside the no-predecessor case, and the script told them
apart from the dispatch context alone, with no indirect signal.
