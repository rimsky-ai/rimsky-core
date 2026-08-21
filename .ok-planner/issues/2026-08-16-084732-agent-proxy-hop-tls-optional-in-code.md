---
issue: agent-proxy-hop-tls-optional-in-code
kind: audit
category: conflicting
artifacts:
  - concept:host-agent-proxy
  - concept:host-agent
  - concept:peer-auth
status: promoted
sprint: 2026-08-21-intake-drain-and-concept-repair.md
opened: 2026-08-16T08:47:32Z
---

# The corpus says the agent-to-proxy hop is always TLS; the code defaults it to plaintext

A developer's host agent connects to a deployment through the host-agent proxy, carrying the developer's api-key. Four artifacts say rimsky serves that hop over TLS against a pinned deployment CA, unconditionally: the proxy concept, the host-agent concept, the peer-auth concept and the proxy-TLS decision. The decision's own rationale is that sending the api-key in plaintext there exposed it. In the code both ends default to plaintext. The proxy serves TLS only from a mounted keypair or an enrolled mutual-TLS leaf. A unit test names the plaintext default as the intended local-dev convenience. The agent's TLS switch and CA path default off. The corpus makes the other peer boundary, service-to-service mutual TLS, explicitly opt-in, so this hop's unconditional wording reads as a deliberate distinction that the code does not honour.

A credential crosses this hop in cleartext by default. The corpus itself singles that hop out as WAN-shaped.

## Options

- Document the plaintext dev default and make TLS on this hop opt-in like the peer boundary; cost: the security posture the decision argued for becomes a recommendation.
- Harden the code so the agent-facing listener requires TLS, with a self-signed local CA for dev; cost: zero-config local dev needs a certificate story.
- Split by deployment mode: allow plaintext only when the deployment itself runs in the zero-config posture, and require TLS otherwise; cost: a posture switch to specify and test.

The ruling decides whether this hop is TLS by promise or by option.

## Ruling

> Recommended ruling (/verify-issues): Keep the promise and change the default. The proxy's agent-facing listener requires TLS, with a locally generated CA in the zero-config posture (the agent already generates one for its own loopback). An explicit insecure switch is the only way to get plaintext.
>
> Rationale: four artifacts and the decision's own threat statement agree the api-key must not cross this hop in the clear. The code's test names the plaintext default as convenience, not design. Flip case: if the owner decides that operators reach the proxy only over an already-encrypted network such as a mesh or tunnel, then documenting plaintext-by-default and stating that assumption is the honest text.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
