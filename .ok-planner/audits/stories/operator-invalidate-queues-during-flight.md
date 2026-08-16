---
audit: operator-invalidate-queues-during-flight
artifact: story:operator-invalidate-queues-during-flight
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:46Z
---

# An operator invalidate against an in-flight node queues a re-run instead of dropping or disrupting it

Supported: a run through the control API of an all-in-one deployment held a
worker's run in flight at a pause-mode pre-dispatch breakpoint and invalidated
that same worker through the debug-override channel. The call was accepted,
reporting one run mutated, and a second worker run appeared queued; the run
already in flight was the same run in the same state, dispatched once. Releasing
the hit let the in-flight run settle successfully, and only then did the queued
run dispatch — the second breakpoint hit named it, and its dispatch follows the
first run's completion in the event sequence. Both runs reached success. Eight
checks, none failing.
