---
decision: envelope-type-discriminator
status: as-is
---

# Envelope type discriminator

## Choice

The message envelope's `kind` field renames to `type` and carries the message type-path (matching the receiver's subscription target and the emitter's `emits_message:` value). The publisher-subscription's `message_kind` field similarly renames to `message_type`.

## Rationale

The envelope's `kind` carried one value providing no information once messages are typed. The rename aligns the wire vocabulary with the conceptual vocabulary; one field, one purpose.
