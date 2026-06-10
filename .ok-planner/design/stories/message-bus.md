---
story: message-bus
status: as-is
---

# Sender emits idempotent messages into instance bus

## Role

As an operator or publisher, I can emit messages into a live instance's bus with a mandatory dedup key, see those messages in the instance's message history, retrieve a specific one by ID, and have a replay return the original message without producing a duplicate, so that downstream nodes consume the bus reliably and no replay slips through.

## Capability

Reliable message emission into a live instance's bus, with mandatory idempotency, per-sender isolation, and observable history.

## Business value

Downstream nodes consume the bus reliably without duplicate processing on retry; cross-sender isolation guarantees one party's emits never replay against another's.

## Acceptance

A sender (operator or publisher, via the control-api `POST /instances/{id}/messages` or its MCP equivalent) emits a message carrying a dedup key; the message is persisted and visible in the instance's message history. A second emission with the same key returns the original message identifier and produces no second envelope. A request with no dedup key is refused. Senders with structurally distinct identities (operator vs. publisher; one operator key vs. another) do not replay each other when they happen to choose the same dedup key.

## Falsifier

A second emission with the same key produces a second envelope, OR the no-key request is silently accepted, OR a publisher named the same as an operator-sender replays the operator's emit.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
