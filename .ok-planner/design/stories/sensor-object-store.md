---
story: sensor-object-store
status: as-is
---

# Operator wires object-store-driven message

## Role

As an operator wiring an object-store-driven message into a workflow, I can use the bundled object-store sensor to poll a bucket-and-prefix at a fixed interval, emit a message per newly-discovered object (with the object's metadata surfaced into the message payload), and persist discovery state so restarts don't re-emit objects already discovered, so that I react to new objects landing in an external store without writing a custom publisher.

## Capability

Bundled object-store sensor publisher: bucket-and-prefix polling; per-object discovery message with metadata payload; durable discovery state across restart; pluggable object-store backend kinds.

## Business value

Operators react to new objects landing in an external store without writing a custom publisher; restart doesn't re-emit objects already discovered.

## Acceptance

An object-store-sensor instance polling a real bucket and prefix discovers a new object dropped after the last poll and emits exactly one message carrying that object's metadata; downstream nodes consume the message; a process restart preserves discovery state and doesn't re-emit objects already discovered. Backend kinds are pluggable.

## Falsifier

Restart re-emits already-discovered objects, OR the configured backend is ignored, OR metadata in the emitted message is canned.

## Proof

Executable proof.
