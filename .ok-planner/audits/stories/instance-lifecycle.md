---
audit: instance-lifecycle
artifact: story:instance-lifecycle
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:20:00Z
---

# Create, watch, pause, resume, force-terminate and remove an instance

Supported. Against a zero-config all-in-one deployment, all 6 runtime controls
the story names answered in one run: create returned a live instance with its
node graph materialized; progress was readable while the node ran, through the
event log's work-completed record, the per-node run counters, and the settling
signal on the status view; pause reported and read back as paused and held a
posted message undelivered with no work running, and resume released it so the
held work ran; the terminate route and `instance kill --force` each stamped an
instance terminated; and `instance delete` removed both records from the instance
list. Intervening on a wedged instance is the terminate path, exercised here on a
healthy instance since the promise is that the operator can force termination,
not that termination requires a wedge.
