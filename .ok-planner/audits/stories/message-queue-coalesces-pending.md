---
audit: message-queue-coalesces-pending
artifact: story:message-queue-coalesces-pending
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# Two instances of one template, one coalescing its queue and one keeping it

Supported. Against a zero-config all-in-one deployment, two instances of a
template that defaults to coalescing ran the same burst: one named no mode at
create and reported coalesce, the other named backlog and reported backlog, so
the choice is the operator's per instance. With the first frame held open on a
pause-mode breakpoint, four numbered wakes arrived; the coalescing instance had
cancelled the two it fell behind on and kept the newest, and the backlog
instance had all three pending and none cancelled. After the drain the
coalescing instance had delivered wakes 1 and 4 across two frames, never
delivering a cancelled one, and its node saw those two payloads; the backlog
instance delivered all 4 across four frames and its node saw every payload. Both
of the story's outcomes therefore hold, each on the mode the operator chose for
it, and a create naming a third mode is refused.
