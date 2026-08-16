---
assessment: message-queue-coalesces-pending--backlog-keeps-every-wake
subject: story:message-queue-coalesces-pending
way: backlog-keeps-every-wake
release: d977250c
outcome: held
warrant: experiment:message-queue-coalesces-pending
---
# An instance that keeps every pending wake, chosen per instance

The sibling instance in the same run overrode the template's default `catalog:template-keys/message_queue_mode` at create time, which is the per-instance half of the promise: two instances of one template ran the same four-wake burst under different modes. While the first frame was held, the overriding instance had kept all four wakes pending; after the drain it ran all four in four separate frames, so nothing was dropped. A mode outside the two the product offers is refused at create rather than silently defaulted, so the choice is a closed one an operator can rely on.

## Unverified remainder

The choice was exercised at four queued wakes on one template. The way does not establish the backlog instance's behaviour under a much deeper backlog, nor whether the queue is bounded.
