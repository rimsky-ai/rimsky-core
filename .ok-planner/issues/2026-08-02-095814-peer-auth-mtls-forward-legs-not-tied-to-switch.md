---
issue: peer-auth-mtls-forward-legs-not-tied-to-switch
kind: audit
category: decision-drift
artifacts:
  - story:peer-auth-mtls-mutual
  - decision:peer-auth-mtls
status: verified
opened: 2026-08-02T09:58:14Z
---

# Flipping `peer_auth: mtls` leaves the forward dispatch legs and the control API's own port in plaintext

The corpus sells mutual TLS as one config flip: set `cfg:peer_auth: mtls` (plus a CA key) and "every internal service↔service connection" becomes mutually authenticated — the story, the decision, and the peer-auth concept all make the same claim (`story:peer-auth-mtls-mutual`, `decision:peer-auth-mtls`, `concept:peer-auth`). The code delivers roughly half of it. The certificate authority, the enrollment exchange, and the supervisor's async-callback return leg genuinely key off the flip and are correctly mTLS'd. But the six forward dispatch dial sites — claim-producer, executor over both transports, publisher, data-processing, validation, and the observability handshake — each gate TLS on that peer entry's own `tls` key, which defaults to off and is never tied to `peer_auth` (`code:lib/control/config/claim_producers.go::parseTLSMode`). And the control API's own inbound listener never switches to TLS under this mode, so services' publish-back calls and the enrollment endpoint itself are plaintext on the server side.

The operator who trusts the one-flip framing gets a deployment where the mTLS machinery is live but every actual dispatch and the control plane's own port are unencrypted — unless they also set `tls: required` on every peer entry, a second knob nothing tells them about. Every existing mTLS dispatch test sets both knobs explicitly, which is how the gap survived: no test exercises the one-flip posture, and the bundled-service harness option built for it has zero call sites.

The ruling decides whether the flip becomes real or the corpus retreats to describing the two-knob shape.

## Options

- Make `peer_auth: mtls` imply `tls: required` on every internal peer entry (per-entry overridable) and TLS-wrap the control API's listener under this mode, plus a real end-to-end bundled-service mTLS test. Cost: a security-relevant behavior change — the control API listener needs a server-certificate source, and anyone already running the flip without per-peer `tls` keys gets a breaking posture change (legal pre-v1, but worth the flag).
- Amend all three artifacts to the two-knob reality and the separately-configured listener. Cost: walks back an explicit security promise — the story's "pay nothing when I don't need it" framing implies its inverse buys full coverage, and it wouldn't.

## Ruling

> Recommended ruling (/verify-issues): make the flip real — `peer_auth: mtls` implies `tls: required` for every internal peer entry unless a peer explicitly overrides it, the control API's inbound listener serves TLS under this mode, and the dormant harness option gets wired into a real end-to-end bundled-service test so the posture stays proven.
>
> Rationale: three artifacts independently promise one-flip coverage, which is strong evidence the promise *is* the intent and the two-knob shape is drift, not design; a security switch that silently half-applies is the worst of both options, and pre-v1 is exactly when the breaking correction is cheap. The listener's certificate sourcing is the real work, and it belongs to the same deployment CA the flip already stands up. Flip case: if the control-API listener turns out to need to stay plaintext for a load-balancer-terminated topology, keep the implied per-peer default but document the listener as the named, deliberate exception — a one-exception promise beats a two-knob one.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
