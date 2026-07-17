---
story: sensor-object-store
status: as-is
---

# Operator wires object-store-driven message

## Role

As an operator wiring an object-store-driven message into a workflow, I can use the bundled object-store sensor to poll a bucket-and-prefix at a fixed interval, send a message per newly-discovered object (with the object's metadata surfaced into the message payload), and persist discovery state so restarts don't re-send objects already discovered, so that I react to new objects landing in an external store without writing a custom publisher.

## Capability

Bundled object-store sensor publisher: bucket-and-prefix polling; per-object discovery message with metadata payload; durable discovery state across restart; pluggable object-store backend kinds.

## Business value

Operators react to new objects landing in an external store without writing a custom publisher; restart doesn't re-send objects already discovered.

## Acceptance

An object-store-sensor instance polling a real bucket and prefix discovers a new object dropped after the last poll, advances its discovery watermark past that object, and attempts to send a message carrying that object's metadata at most once; downstream nodes consume the message on a successful send; a process restart preserves discovery state and doesn't re-send objects already discovered, even one whose send previously failed. Backend kinds are pluggable.

## Falsifier

Restart re-sends already-discovered objects, OR the configured backend is ignored, OR metadata in the sent message is canned.

## Proof

Executable proof.
