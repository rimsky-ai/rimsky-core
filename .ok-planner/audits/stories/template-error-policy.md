---
audit: template-error-policy
artifact: story:template-error-policy
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:20:00Z
---

# The four declared error-routing actions are honoured at the failing dispatch

Supported. Against a zero-config all-in-one deployment, all 4 actions of the
vocabulary the story names were declared on the same node against the same
deterministic executor error class and each was honoured: `pass` settled the run
fresh while the settling signal still named the error class, `give_up` settled it
failed, `retry` under a declared cap of two emitted one retry signal per attempt,
took no third, and settled the run failed once the budget was spent, and
`release_and_requeue` emitted a release-and-requeue signal per failure and put
the run back for another dispatch without settling it either way. The measurement
covers the executor-emitted error site, which is where all four actions are
reachable from a template alone.
