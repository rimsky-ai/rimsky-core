---
story: sensor-webhook
status: as-is
---

# Operator wires inbound-webhook message

## Role

As an operator wiring an inbound-webhook-driven message into a workflow, I can use the bundled `sensor-webhook` to expose configured HTTP routes; inbound POSTs translate to messages routed into rimsky against the subscription's target instance, so that external systems trigger rimsky nodes via webhooks without polling overhead.

## Capability

Bundled `sensor-webhook` publisher: HTTP route exposure under a configured path-prefix; inbound POST translated to message into the target instance; acknowledgment after rimsky has persisted the message.

## Business value

External systems trigger rimsky nodes via webhooks without polling overhead; the webhook receipt is acknowledged only after the message has actually landed in rimsky.

## Acceptance

A `sensor-webhook` instance subscribed with a configured path-prefix exposes HTTP routes under that prefix; a real inbound POST to a route reaches a message in the targeted rimsky instance with the request body translated into the message payload; the inbound request is acknowledged with success once rimsky has persisted the message.

## Falsifier

Inbound POST acknowledged before the message is persisted in rimsky, OR the path-prefix filter is declared but unused, OR the request body translation is canned.

## Proof

Executable proof.
