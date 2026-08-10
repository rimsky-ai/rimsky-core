---
audit: work-completed-emitted
artifact: story:work-completed-emitted
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# Every finished dispatch pairs, and the unpaired one is the unfinished one

Supported. One instance was driven through six dispositions — success, error,
error-then-retry, a park that resumed and succeeded, a park left outstanding,
and a built-in executor's dispatch — and the ledger was read back through the
public event feed. Joining on the dispatch id both kinds carry, all five
dispatches that reached a terminal had a work_completed, none named a dispatch
that never started, and every completion named its terminal kind, distinguishing
success from failure. Durations fell out of the two timestamps alone, all five
non-negative. The pairing is by dispatch, not by event: a park that resumes
re-announces the start, so that dispatch carried two starts and one completion.
The one dispatch with no completion was the one still parked — the same node the
park roster still held — which is exactly what a did-everything-finish audit
needs the ledger to say.
