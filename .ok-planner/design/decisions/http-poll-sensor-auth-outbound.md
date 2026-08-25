---
decision: http-poll-sensor-auth-outbound
---

# The http-poll sensor takes the webhook sensor's `auth` block outbound

## Choice

The bundled http-poll sensor takes the same `auth` block as the bundled webhook sensor (see `decision:webhook-auth-required`) and applies it to its own outbound poll. The block takes two modes: `secret_header` (send a configured header on every poll) and `none`. It does not take `hmac`. A subscription whose `auth` block names any other mode is refused at bind time. A poll subscription with no `auth` block sends no credentials.

## Rationale

The http-poll sensor shares the block so that an operator who has learned one `auth` shape writes the same shape for both sensors. `hmac` is absent outbound because a poll is a GET with no body. No upstream defines a signature over a request rimsky originates. Omission is legal outbound because the poll is rimsky's own request to an upstream the operator chose. An unauthenticated poll exposes nothing of rimsky's, so the fail-loud polarity that protects the webhook sensor's inbound port has nothing to protect here. The sensor refuses an unknown mode because it cannot apply the block. A block the sensor drops is a credential the operator believes it sends.

## Alternatives

- A poll-side `hmac` over method, URL, and timestamp — rejected: no upstream verifies such a signature, so the mode would describe a request nobody checks.
- Fail-loud omission on the poll side, as the webhook sensor has — rejected: the poll is outbound to an upstream the operator chose, and forcing `none` there protects nothing.
- A poll-specific credential key outside the `auth` block — rejected: a second shape for one job.
