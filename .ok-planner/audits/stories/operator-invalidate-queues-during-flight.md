---
audit: operator-invalidate-queues-during-flight
artifact: story:operator-invalidate-queues-during-flight
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:30:00Z
---

# An invalidate against an in-flight node queues behind it

Supported. Against a zero-config all-in-one deployment, a pause-mode
pre-dispatch breakpoint held the worker's only run in flight, and the operator
invalidated that same node while it sat there. The invalidate was accepted and
produced a second worker run in `stale`, so it was not dropped; the run already in
flight was still the same run in `running`, and the worker had been dispatched
only once, so it was not disturbed. Releasing the hit let the in-flight run
complete successfully, and only then did the queued run dispatch — the event log
puts the second dispatch after the first run's completion — and it too reached
success. Both halves of the story's benefit were exercised in the one run: the
action was neither dropped nor destructive.
