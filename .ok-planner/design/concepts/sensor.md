---
concept: sensor
---

# Sensor

## What it is

A sensor is a class of `concept:publisher` implementation that observes external state. Sensors poll, listen, or otherwise watch some out-of-rimsky substrate (clock, HTTP endpoint, object-store prefix, webhook port) and publish messages into rimsky when the watched substrate changes.

Sensors implement the `concept:publisher` protocol — a capabilities handshake, subscribe, unsubscribe, and list-subscriptions — and POST message envelopes to the universal operator message-send endpoint, identifying themselves as publishers and presenting the per-subscription capability token.

The bundled reference implementations are sensors-by-construction; they share no protocol-level surface with rimsky beyond the publisher protocol itself.

## Purpose

To bridge external substrate changes into rimsky's instance frames without requiring rimsky-core knowledge of the substrate. A sensor observes the substrate, builds an opaque payload, and hands it to rimsky as a generic `concept:message`; rimsky routes the message through the existing cascade machinery.

## Boundaries

Owns: the watching loop, the per-substrate dialect, the in-binary per-subscription state (next fire time, body hash, watermark cursor, last idempotency key), and the message-envelope construction at fire time.

Does NOT own: the wire protocol (that's `concept:publisher`), the message envelope shape (that's `concept:message`), the per-instance binding state (that's `concept:publisher-subscription`, stored in the rimsky-side publisher-subscription ledger), or the deployment-tier replica posture (that's `concept:replica`).

Adjacent: `concept:publisher` (sensors implement it), `concept:publisher-subscription` (sensors hold its publisher-side state in their own per-binary state DB), `concept:message` (sensors send them), `concept:replica` (sensor binaries are single-replica by that concept's posture), `concept:peer-auth` (the webhook sensor's inbound-auth requirement realizes the public-web ingress boundary).

## Invariants

- Sensors are deployed as standalone services advertised in the publisher service registry of `concept:rimsky-yml`. Same deployment model as `concept:claim-producer` or `concept:executor`.
- Templates declare sensors as publisher entries (sensors ARE publishers); at instance creation, rimsky resolves each publisher entry's config via parameter substitution and calls the publisher protocol's subscribe verb.
- At instance termination, rimsky calls the publisher protocol's unsubscribe verb for each registered publisher-subscription.
- Each send constructs a message envelope (see `concept:message`) and posts it to the universal message-send endpoint under an idempotent send. Inert payload per invariant: 24.
- Sensors observe; they do not interpret. Payload bytes flow through rimsky unread until a consumer's substitution leaf walks into them.
- Single-replica per `concept:replica` — operators run one pod per sensor binary; rimsky does not coordinate multi-replica fan-in.
- Emission-failure semantics: a message post rejected by the control API with a permanent 4xx is **dropped, not retried** — the sensor logs loudly (`<sensor>.message_rejected_dropped`, naming the subscription and status) and advances its consumed-state exactly as a successful post would (body-hash cursor, fire-window cursor, watermark, idempotency watermark). Transient failures — transport errors, 5xx, and the retryable 4xx carve-out 408/429 — do not advance state, so the observation is re-attempted on the next cycle. Rationale: retry-forever was never durable (a newer observation supersedes via the hash/state dedup) and it wedges misconfigured watches into permanent retry. The permanent/transient split is machine-detectable via the publisherkit typed rejection error; 408 and 429 retry within the send's attempt budget and surface as transient on exhaustion.
- The webhook sensor requires per-subscription authentication, configured as exactly one of `hmac` (HMAC-SHA256 over the raw body, with an optional timestamp header and replay window), `secret_header` (constant-time compare of a configured header), or `none` (explicit opt-out). Polarity is fail-loud: a subscription with no `auth` block is refused at bind time — the insecure `none` mode must be typed explicitly, mirroring the closed-by-default polarity of the bundled-image egress guard. This closes unauthenticated message injection and forged-idempotency-key pre-seeding on the public-web ingress boundary (see `concept:peer-auth`).
