---
story: message-queue-coalesces-pending
---

# Coalesce-mode instances drop stale wakes from the message queue

## Story

As an operator whose instance receives wake messages faster than it runs frames, I can choose per instance whether pending messages all survive to their own frames or only the newest survives (`concept:instance`), so that a slow instance tracks the latest wake instead of falling arbitrarily far behind — while instances whose messages carry payload keep every one.
