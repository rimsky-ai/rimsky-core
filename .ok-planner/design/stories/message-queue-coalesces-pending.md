---
story: message-queue-coalesces-pending
status: as-is
---

# Coalesce-mode instances drop stale wakes from the message queue

## Role

As an operator whose instance is slow relative to its message cadence, I can configure the instance's message queue to coalesce mode so that when N wake messages accumulate for the instance while a frame is running, only the newest is delivered — the earlier N-1 are dropped. Under the default backlog mode, all N messages are delivered in order across N frames. The coalesce vs backlog choice belongs to the instance's queue, not to individual messages.

## Capability

Each instance carries a `message_queue_mode` setting whose value is one of `backlog` (default) or `coalesce`. Under `backlog`, every message inserted into the instance's queue survives until its frame opens and delivers it. Under `coalesce`, at the moment a new message is inserted for the instance, all prior pending (undelivered, non-cancelled) messages for that instance are marked cancelled in the same transaction — leaving the newly-inserted message as the only pending one. The setting is declared on the template as `message_queue_mode`; instance creation materializes the value onto the instance row. The setting is per-instance and applies uniformly to every message type on that instance's queue.

## Business value

Under frame isolation each message opens one frame and only one frame runs at a time per instance. If the instance's message-sender cadence outpaces its frame runtime, un-delivered messages pile up in the instance's queue and a slow instance falls arbitrarily far behind. `coalesce` lets an operator declare "for this instance, I only care about the latest wake — every older one is stale by the time we get to it," bounding the queue at one pending message regardless of upstream cadence. `backlog` keeps the traditional every-message-runs semantic for instances whose messages carry payload data downstream depends on.

