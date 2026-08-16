---
experiment: message-queue-coalesces-pending
commit: PENDING
---

# Choosing per instance whether pending wakes survive

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`). One template declares
`message_queue_mode: coalesce` and one node reacting to a wake message; the wake
type declares a body schema and every wake carries a numbered payload. Two
instances of that template run the same burst: the first names no mode at
create, the second names `backlog`. Each instance holds its first frame open on
a pause-mode `before_dispatch` breakpoint while four numbered wakes arrive, then
resumes every breakpoint hit until the queue drains.

## What was observed

The first instance reports `coalesce`, the second reports `backlog`, so the mode
is chosen per instance over the template's default. While the first frame was
held, the coalescing instance had cancelled wakes 2 and 3 and kept 4 pending;
the backlog instance had all three pending and none cancelled.

After the drain, the coalescing instance had delivered wakes 1 and 4 across two
frames and never delivered a cancelled wake; its node saw the payloads 1 and 4.
The backlog instance delivered all four across four frames and its node saw all
four payloads. A create naming a mode outside the two is refused.

Coalescing cancels payload-carrying wakes as readily as bare ones: the two the
first instance dropped both carried a body conforming to the declared schema.
That matches `concept:instance`, which states the mode applies uniformly to
every message type on the queue. Keeping every payload is what choosing
`backlog` buys, not a property the payload itself confers.

Twelve checks, none failing.

RESULT: PASS
