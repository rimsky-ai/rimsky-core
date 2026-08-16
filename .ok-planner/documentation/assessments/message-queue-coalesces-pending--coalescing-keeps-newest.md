---
assessment: message-queue-coalesces-pending--coalescing-keeps-newest
subject: story:message-queue-coalesces-pending
way: coalescing-keeps-newest
release: d977250c
outcome: held
warrant: experiment:message-queue-coalesces-pending
---
# An instance that keeps only the newest pending wake

The audit ran two instances of one template whose declared `catalog:template-keys/message_queue_mode` is the coalescing one, firing the same burst of four numbered wakes at each while its first frame was held open, so the queue genuinely backed up rather than being raced. The instance that named no mode took the template default and so ran coalescing. While the frame was held, that instance had already cancelled the two middle wakes and kept only the newest. After the drain it had run the first wake and the latest across two frames and had never delivered a cancelled wake. That is the benefit exactly: the slow instance tracked the latest wake instead of falling further behind. Twelve checks across this way and its sibling, none failing.

## Unverified remainder

Coalescing cancels payload-carrying wakes as readily as bare ones — every wake in this run carried a body conforming to the declared schema, and two were dropped. An operator who needs every payload must select the other mode rather than relying on payloads being exempt.
