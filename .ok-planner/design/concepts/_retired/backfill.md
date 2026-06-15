---
concept: backfill
status: retired
aliases: []
---

# Backfill

Backfill is a use case of the typed-message machinery: a template declares a message type whose body carries the partition-request override; a fan-out node's `partition_request:` substitutes from the message body. No dedicated primitive. → `concept:message`, `concept:message-schema`, `concept:fan-out`.
