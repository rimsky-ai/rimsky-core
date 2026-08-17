---
decision: webhook-auth-required
aliases:
  - webhook-auth-fail-loud
---

# The webhook sensor requires per-subscription auth, fail-loud

## Choice

The bundled webhook sensor requires per-subscription authentication, configured as exactly one of `hmac` (HMAC-SHA256 over the timestamp header's value joined to the raw body — the timestamp header is required and a subscription omitting it is refused at bind time; only the replay window is optional), `secret_header` (constant-time compare of a configured header), or `none` (explicit opt-out). Polarity is fail-loud: a subscription with no `auth` block is refused at bind time — the insecure `none` mode must be typed explicitly (see `concept:sensor`).

## Rationale

An unauthenticated webhook port on the public web accepts message injection and forged-idempotency-key pre-seeding from anyone who can reach it. Requiring the operator to name an auth mode — and to type `none` deliberately when they truly want none — makes the insecure choice visible rather than the silent default. Requiring the timestamp header inside HMAC mode is the same polarity one level down: the timestamp is signed material, so replay protection arrives with the mode rather than as a separate opt-in. This mirrors the closed-by-default polarity of the bundled-image egress guard, and is the opposite polarity from the claude-agent allowlists (unset = open) precisely because this is an inbound public-web boundary, not an internal policy knob.

## Alternatives

- **Default `none` (auth optional)** — rejected: an omitted auth block would silently expose the port, exactly the failure mode fail-loud exists to prevent.
- **An optional timestamp header in HMAC mode** — rejected: a signature over the body alone replays forever, and making replay protection opt-in leaves the weaker configuration one omission away.
- **A single fixed auth scheme** — rejected: HMAC-over-body and shared-header schemes are both common among upstream webhook producers, so the sensor offers both plus an explicit opt-out.
