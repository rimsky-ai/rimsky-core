---
story: sensor-webhook
status: as-is
---

# Operator wires inbound-webhook message

## Story

As an operator wiring an inbound-webhook-driven message into a workflow, I can use the bundled webhook sensor to expose configured HTTP routes; inbound POSTs translate to messages routed into rimsky against the subscription's target instance, so that external systems trigger rimsky nodes via webhooks without polling overhead.

Bundled webhook sensor publisher: HTTP route exposure under a configured path-prefix; per-subscription inbound authentication (one of `hmac`, `secret_header`, or an explicit `none`, refused fail-loud at bind time when the `auth` block is absent); inbound POST authenticated then translated to message into the target instance; acknowledgment after rimsky has persisted the message.

External systems trigger rimsky nodes via webhooks without polling overhead; the webhook receipt is acknowledged only after the message has actually landed in rimsky. Requiring per-subscription auth closes unauthenticated message injection and forged-idempotency-key pre-seeding on the public-web ingress, and making the insecure `none` mode explicit keeps an unauthenticated port from being the silent default.
