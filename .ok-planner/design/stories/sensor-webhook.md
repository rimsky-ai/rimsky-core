---
story: sensor-webhook
status: as-is
---

# Operator wires inbound-webhook message

## Role

As an operator wiring an inbound-webhook-driven message into a workflow, I can use the bundled webhook sensor to expose configured HTTP routes; inbound POSTs translate to messages routed into rimsky against the subscription's target instance, so that external systems trigger rimsky nodes via webhooks without polling overhead.

## Capability

Bundled webhook sensor publisher: HTTP route exposure under a configured path-prefix; per-subscription inbound authentication (one of `hmac`, `secret_header`, or an explicit `none`, refused fail-loud at bind time when the `auth` block is absent); inbound POST authenticated then translated to message into the target instance; acknowledgment after rimsky has persisted the message.

## Business value

External systems trigger rimsky nodes via webhooks without polling overhead; the webhook receipt is acknowledged only after the message has actually landed in rimsky. Requiring per-subscription auth closes unauthenticated message injection and forged-idempotency-key pre-seeding on the public-web ingress, and making the insecure `none` mode explicit keeps an unauthenticated port from being the silent default.

## Acceptance

A webhook-sensor instance subscribed with a configured path-prefix and an `auth` block exposes HTTP routes under that prefix; a correctly-signed (or correctly-headered) inbound POST to a route reaches a message in the targeted rimsky instance with the request body translated into the message payload, acknowledged with success once rimsky has persisted the message; an unsigned or mis-signed POST is rejected and produces no message; a subscription declared with no `auth` block is refused at bind time.

## Falsifier

Inbound POST acknowledged before the message is persisted in rimsky, OR the path-prefix filter is declared but unused, OR the request body translation is canned, OR an unsigned/mis-signed POST produces a message, OR a subscription with no `auth` block binds successfully.

## Proof

Executable proof.
