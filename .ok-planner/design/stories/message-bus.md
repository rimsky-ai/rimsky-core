---
story: message-bus
---

# Sender sends idempotent messages into instance bus

## Story

As an operator or publisher, I can send messages into a live instance's bus with a mandatory dedup key, see those messages in the instance's message history, retrieve a specific one by ID, and have a replay return the original message without producing a duplicate, so that downstream nodes consume the bus reliably and no replay slips through.
