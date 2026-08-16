---
audit: webhook-auth-required
artifact: decision:webhook-auth-required
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:09:41Z
---

# The webhook sensor's fail-loud auth requirement, and its HMAC mode's shape

Unsupported, on one clause of the Choice. The fail-loud core holds: the bundled webhook sensor validates an auth block at bind time and refuses the subscription outright when it is absent, when the mode is empty, or when the mode is not one of the three the decision names; its advertised capability schema likewise marks the auth object required. All three modes exist and behave as described — a constant-time compare of a configured header, an explicit no-op opt-out, and an HMAC-SHA256 verification — and unit tests cover each mode's accept and reject paths plus the two refusal cases, with an end-to-end scenario confirming an unsigned inbound post is rejected. What the code does not carry is the Choice's description of the HMAC mode as signing "the raw body, with an optional timestamp header and replay window": the timestamp header is mandatory, refused at bind time when absent with an error stating that replay protection is not optional, and it is the first half of the signed material, the signature covering the timestamp and the body joined rather than the body alone. A test is named for exactly that refusal, so the divergence is deliberate on the code's side and stale on the decision's. Only the replay window itself is genuinely optional, defaulting when unset. Checked all three declared auth modes against the sensor's bind-time validator, its request-time authenticator, and its own test suite.
