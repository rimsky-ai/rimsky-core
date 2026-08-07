---
issue: proxy-unsustainable-mtls-posture
kind: audit
category: config-surface
artifacts:
  - concept:peer-auth
  - concept:host-agent-proxy
status: promoted
opened: 2026-08-06T06:49:14Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# The host-agent proxy never joins the mutual-TLS trust domain it is supposed to live inside

When a deployment turns on rimsky's internal mutual-TLS trust domain
(`peer_auth: mtls`), every standing service enrolls itself with the
control API, receives a short-lived certificate — leaves live 24 hours
(`lib/foundation/pki/ca.go:24`) — and renews automatically. The
host-agent proxy, the standing service that fronts host agents for the
supervisor, never enrolls: it imports none of the enrollment machinery.
Its only TLS story is a hand-supplied static certificate pair
(`cmd/rimsky-host-agent-proxy/tls.go`), and its listener never requests
client certificates (`ClientAuth: NoClientCert`, unconditionally). An
operator running the trust domain as designed therefore has one
internal hop that is either plaintext or secured by a certificate that
expires every day with nothing to renew it — and even a hand-minted
leaf cannot make the supervisor-facing leg mutual, because the same
listener that must stay non-mutual toward agents also serves the
supervisor-facing protocols.

The corpus commits to the opposite: `decision:peer-auth-mtls` says
every operator-deployed standing service enrolls and both peers of
every internal connection present and verify CA-signed certificates,
and `concept:service` carries the same invariant. The proxy is a live
counterexample. `decision:host-agent-proxy-tls` decided only the
agent-side hops (pinned-CA server TLS toward agents, local mTLS toward
children) and is silent on the supervisor-facing leg.

## Options

- Enroll the proxy through the existing enrollment machinery, renewal
  loop included, and split its serving so the supervisor-facing leg
  verifies client certificates while the agent-facing leg keeps the
  posture its own decision fixed. Cost: real feature work — a second
  listener and a renewal loop.
- Carve a documented exception: long-lived proxy leaves, or a manual
  re-mint cadence. Cost: abandons the uniform short-lived-certificate
  posture for exactly one service.
- Sanction `tls: off` on the proxy's peer entries under mtls and say
  so in the corpus. Cost: one plaintext internal hop in an
  otherwise-mutual domain, contradicting the uniformity the corpus
  claims.

The ruling decides whether the proxy becomes a real member of the
trust domain or gets a documented carve-out.

## Ruling

> Recommended ruling (/verify-issues): make the proxy enroll like
> every other standing service, renewal loop included, and split its
> serving so the supervisor-facing leg verifies client certificates
> while the agent-facing leg keeps its already-decided non-mutual
> posture.
>
> Rationale: the corpus already commits to "every standing service
> enrolls" — both carve-out options spend that invariant to save
> implementation work, and enrollment also dissolves the sibling
> CA-pinning issue's mutual-TLS case for free. Rule this together
> with the missing-CA issue. The flip case: if the proxy is about to
> be re-architected out of the supervisor path, or the trust domain
> itself redesigned, a documented exception is the cheaper bridge.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
