---
story: frame-origin-audit
status: as-is
---

# Operator sees the triggering message for every frame

## Role

As an operator,

## Capability

I can see for every frame what triggered it (an operator message, a publisher message, or a cascade-sent message) through the existing frame observability surface,

## Business value

so that "why did this frame open" is always answerable directly.

## Acceptance

Every frame carries a pointer back to the message ledger entry that triggered it, surfaced through the existing frames-read observability endpoint. No frame in the system has "cascade walker" or "internal" as its origin. Looking up a frame returns the originating message's sender, type, and sender kind plus the message id; the message body is fetched from the message-read endpoint by that id (its own read permission — bodies can be large and are not inlined into frame listings).

## Falsifier

A frame appears without an originating message reference; OR an internal-to-runtime path creates a frame in any code path.

## Proof

Demo. Every frame in a representative end-to-end run (including back-edge cycles and self-drain) has an originating message visible through the observability surface.
