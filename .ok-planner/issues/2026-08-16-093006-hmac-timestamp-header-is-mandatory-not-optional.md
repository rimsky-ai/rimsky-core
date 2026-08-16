---
issue: hmac-timestamp-header-is-mandatory-not-optional
kind: audit
category: conflicting
artifacts:
  - decision:webhook-auth-required
  - concept:sensor
status: verified
opened: 2026-08-16T09:30:06Z
---

# Two artifacts describe the webhook HMAC mode with an optional timestamp; the sensor requires it

The webhook sensor accepts inbound HTTP and authenticates it; in HMAC mode, the webhook-auth decision and the sensor concept both say the signature is over the raw body "with an optional timestamp header and replay window". The sensor refuses an HMAC subscription that omits the timestamp header at bind time (its own reason: the timestamp is part of the signed material, so replay protection is mandatory) and signs the timestamp, a separator, and the body — only the replay window is optional. An upstream built from the prose signs the body alone and omits the header, and every request is rejected. The ruling fixes both artifacts.

## Options

- Rewrite the HMAC clause in both artifacts: timestamp header required, signature over timestamp + separator + body, replay window optional; cost: none.
- Loosen the sensor to match the prose; cost: weakens replay protection against the sensor's own fail-loud design.

The ruling corrects the description; the code is deliberate.

## Ruling

> Generated ruling (/verify-issues): Rewrite the HMAC clause identically in the webhook-auth decision and the sensor concept — the timestamp header is required, the signature covers the timestamp header value joined to the raw body, and only the replay window is optional. Forced by the current-state-only rule; the code carries its own security rationale and a refusal test. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
