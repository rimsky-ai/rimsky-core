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

## Acceptance

An instance is provisioned from a template declaring `message_queue_mode: coalesce`. The instance's first frame is running. Five wake messages arrive for the instance during the run. The test asserts that the queue holds exactly one pending message (the fifth); the earlier four are marked cancelled. After the running frame settles, exactly one new frame opens, triggered by the fifth message. The lineage shows two frames for the instance, and the second frame's trigger message is the fifth.

An instance is provisioned from a template declaring `message_queue_mode: backlog` (or omitting the setting). Same five-message sequence. The test asserts all five remain pending; none are cancelled. Six frames run in sequence (the original plus five triggered by each queued message).

## Falsifier

Coalesce-mode instance: the queue grows past one pending message during a running frame (observable by counting pending messages for the instance while a frame is running). OR a second frame settles with a payload from message 1–4 rather than message 5 (observable by inspecting the second frame's trigger message).

Backlog-mode instance: fewer than six frames run (observable by counting frames for the instance after all messages have been processed). OR any message is cancelled without a matching operator or lifecycle event (observable by counting non-delivered messages for the instance).

## Proof

An executable scenario test seeds two instances — one `coalesce`, one `backlog`. Each has its first frame in flight. Five wake messages arrive rapidly for each. The test drives ticks until the instances are quiescent and asserts: coalesce → two frames total, four cancelled messages, second frame's trigger is the fifth message. Backlog → six frames total, zero cancelled messages, each frame's trigger is a distinct message in receipt order.
