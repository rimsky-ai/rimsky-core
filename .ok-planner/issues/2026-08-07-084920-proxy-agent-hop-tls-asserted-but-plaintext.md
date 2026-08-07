---
issue: proxy-agent-hop-tls-asserted-but-plaintext
kind: human
category: conflicting
artifacts:
  - concept:host-agent-proxy
  - concept:peer-auth
  - decision:host-agent-proxy-tls
  - decision:host-agent-proxy-enrollment
status: verified
opened: 2026-08-07T08:49:20Z
github: https://github.com/rimsky-ai/rimsky-core/issues/72
---

# The design record promises encryption on a hop that ships in plaintext

rimsky can run work on machines it does not control — a developer laptop, a
build box — by having a small agent on that machine dial in. The agent does not
talk to the platform directly; it connects to a **proxy** that sits inside the
deployment and forwards work to it. That link crosses whatever network lies
between the machine and the deployment, which makes it the most exposed hop in
the system.

Three places in the design record state that this link is secured by a
certificate issued by the deployment's own certificate authority, and state it
unconditionally: the host-agent-proxy concept, the peer-auth concept's rule of
thumb, and the decision covering proxy TLS.

The proxy serves it in plaintext by default. Its credential builder returns no
credentials at all unless an operator has mounted a certificate and key into the
container by hand
(`cmd/rimsky-host-agent-proxy/tls.go::proxyServerCredentials` — both paths empty
returns nil). When an operator does mount one, it is whatever certificate they
supplied, and the agent verifies it against whatever root they configured on
their side — not against the deployment CA. No test asserts either behavior.

So the code's actual posture is: opt-in, operator-supplied, unrelated to the
platform's own certificate authority, and off unless someone turns it on. The
record's posture is: on, always, chained to the deployment CA. Those are not the
same guarantee, and the gap sits on a security boundary, which is the one place
a reader is most entitled to take the record at face value.

There is also an inconsistency inside the record itself. A separate decision
scopes the proxy's enrollment with the deployment CA to mutual-TLS mode — "under
mutual-TLS peer auth the host-agent proxy enrolls…" — and outside that mode
there is no deployment CA in existence at all. So the unconditional claim cannot
literally hold: either it is implicitly scoped to mutual-TLS mode and worded
loosely, or it describes something that was never built. The record does not say
which.

Worth noting what already exists to build on: in mutual-TLS mode the proxy
already enrolls and already holds a deployment-CA-issued certificate, which it
uses on a separate listener facing the platform's own services. The agent-facing
listener does not use it.

## Options

- **Present the proxy's own enrolled certificate on the agent-facing listener
  whenever the deployment runs mutual TLS**, staying plaintext otherwise. Makes
  the record true within the mode it was always implicitly about, and needs no
  new certificate machinery; the hop stays plaintext in the default mode, which
  is the more common deployment.
- **Encrypt the hop always**, independent of the deployment's peer-auth posture,
  using a certificate the proxy issues for itself. Strongest, and matches the
  unconditional claim as written; costs a second certificate mechanism to build
  and operate.
- **Retract the claim** in all three places and describe the operator-supplied,
  off-by-default posture that actually ships. Cheapest and honest; leaves the
  most exposed hop in the system unencrypted by default.

The ruling decides which security posture rimsky commits to on the agent hop —
and the record follows it, rather than the other way around.

## Ruling

> Recommended ruling (/verify-issues): serve the agent-facing hop with the
> certificate the proxy already enrolls for, whenever the deployment runs mutual
> TLS, and correct the three places in the record to say that plainly. This is
> the smallest change that makes the promise true, and it reuses a credential
> the proxy already holds and already renews — the certificate is sitting there
> being used on the other listener.
>
> Rationale: of the three options only this one closes the gap by building the
> thing rather than by editing the claim, and it does so without inventing a
> second certificate authority to operate. Encrypting unconditionally is the
> stronger posture and the right one to revisit later, but it means standing up
> certificate issuance that works with no deployment CA present, which is a
> larger commitment than a record correction should quietly pull in. Retracting
> the claim is the option to reject: it resolves a contradiction by lowering the
> promise on the system's most exposed link. Whichever way this goes, the three
> record sites must end up saying the same thing as the code and as each other —
> today they do not, and the scoping question the enrollment decision raises has
> to be answered explicitly rather than left to a reader. What would change this
> call: if agents are expected to run across untrusted networks against
> deployments that do *not* run mutual TLS, then the default matters more than
> the reuse and the unconditional option is the right one.
>
> Rule this with `issue:no-way-to-export-deployment-ca-root` and
> `issue:plaintext-enrollment-hop-silently-accepted` — same bootstrap story,
> different ends.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to
accept — the next /plan-sprint carries it, naming the generated/recommended
batches at sign-off. Edit the text to redirect, empty the section to discuss
live, or delete this note to adopt the ruling as your own. -->
