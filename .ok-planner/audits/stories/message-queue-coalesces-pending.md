---
audit: message-queue-coalesces-pending
artifact: story:message-queue-coalesces-pending
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:03:56Z
---

# Choosing per instance whether pending wakes all survive or only the newest does

Supported: the choice exists per instance and both branches behave as the story
needs. Two instances of one template, whose default is the coalescing mode, ran
the same burst of four numbered wakes while their first frame was held open, so
the queue genuinely backed up rather than being raced. The instance naming no
mode took the template default and the sibling naming the other mode overrode it,
which is the per-instance part of the promise. While the frame was held the
coalescing instance had cancelled the two middle wakes and kept only the newest,
and the backlog instance had kept all of them; after the drain the coalescing
instance had run the first and the latest across two frames and never delivered a
cancelled wake, while the backlog instance ran all four in four frames. That is
the benefit exactly — the slow instance tracked the latest wake instead of
falling further behind. A mode outside the two is refused at create. Note that
coalescing cancels payload-carrying wakes as readily as bare ones: every wake in
this run carried a body conforming to the declared schema, and two were dropped.
Twelve checks, none failing.

## Compliance

- The trailing clause ("while instances whose messages carry payload keep every one") reads as a product guarantee that payload-carrying messages are exempt from coalescing, which the product does not make and the run contradicts; the compliant text attributes the outcome to the choice, e.g. "so that a slow instance tracks the latest wake instead of falling arbitrarily far behind, and an instance that must keep every message can choose to".
