---
issue: agent-proxy-hop-tls-optional-in-code
kind: audit
category: conflicting
artifacts:
  - concept:host-agent-proxy
  - concept:host-agent
  - concept:peer-auth
status: verified
opened: 2026-08-16T08:47:32Z
---

# The corpus says the agent-to-proxy hop is always TLS; the code defaults it to plaintext

A developer's host agent connects to a deployment through the host-agent proxy, carrying the developer's api-key. Four artifacts — the proxy concept, the host-agent concept, the peer-auth concept and the proxy-TLS decision — say that hop is served over TLS against a pinned deployment CA, unconditionally; the decision's own rationale is that sending the api-key in plaintext there exposed it. In the code both ends default to plaintext: the proxy serves TLS only from a mounted keypair or an enrolled mutual-TLS leaf, a unit test names the plaintext default as the intended local-dev convenience, and the agent's TLS switch and CA path default off. The corpus treats the other peer boundary (service-to-service mutual TLS) as explicitly opt-in, so this hop's unconditional wording reads as a deliberate distinction that the code does not honour. The ruling decides whether the promise or the default gives.

What is at stake is a credential crossing a network hop in cleartext by default on a hop the corpus itself singles out as WAN-shaped.

## Options

- Document the plaintext dev default and make TLS on this hop opt-in like the peer boundary; cost: the security posture the decision argued for becomes a recommendation.
- Harden the code so the agent-facing listener requires TLS (self-signed local CA for dev); cost: zero-config local dev needs a certificate story.
- Split by deployment mode — plaintext only when the deployment itself is in the zero-config posture, TLS required otherwise; cost: a posture switch to specify and test.

The ruling decides whether this hop is TLS by promise or by option.

## Ruling

> Recommended ruling (/verify-issues): Keep the promise and change the default: the proxy's agent-facing listener requires TLS, with a locally generated CA in the zero-config posture (the agent already generates one for its own loopback), and plaintext available only by an explicit insecure switch.
>
> Rationale: four artifacts and the decision's own threat statement agree the api-key must not cross this hop in the clear; the code's test names the plaintext default as convenience, not design. Flip case: if the owner decides the proxy is only ever reached over an already-encrypted network (a mesh or tunnel), then documenting plaintext-by-default with that assumption stated is the honest text.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
