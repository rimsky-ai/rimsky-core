---
story: message-bus
status: as-is
---

# Sender sends idempotent messages into instance bus

## Role

As an operator or publisher, I can send messages into a live instance's bus with a mandatory dedup key, see those messages in the instance's message history, retrieve a specific one by ID, and have a replay return the original message without producing a duplicate, so that downstream nodes consume the bus reliably and no replay slips through.

## Capability

Reliable message send into a live instance's bus, with mandatory idempotency, per-sender isolation, and observable history.

## Business value

Downstream nodes consume the bus reliably without duplicate processing on retry; cross-sender isolation guarantees one party's emits never replay against another's.

