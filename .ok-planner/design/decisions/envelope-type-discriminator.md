---
decision: envelope-type-discriminator
status: as-is
---

# Envelope type discriminator

## Choice

The message envelope's discriminator field is `type` and carries the message type-path (matching the receiver's subscription target and the sender's declared send type); the publisher-subscription's counterpart field is `message_type`. No separate kind vocabulary exists alongside the type-path.

## Rationale

A separate kind discriminator would carry a single value providing no information once messages are typed — the type-path already discriminates. One field, one purpose, and the wire vocabulary matches the conceptual vocabulary.

## Alternatives

- A `kind` field kept alongside the typed message path — rejected: a single-valued discriminator duplicating the type-path is dead wire weight and a second vocabulary to keep aligned.
