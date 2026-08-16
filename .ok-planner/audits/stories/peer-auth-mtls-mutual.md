---
audit: peer-auth-mtls-mutual
artifact: story:peer-auth-mtls-mutual
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:15:00Z
---

# One configuration change authenticates the internal plane, and the default still costs nothing

Supported. Two stacks from the same image were driven: one with the single
configuration change and the CA key in its environment, one on the untouched
default. After the change the control-API listener refused plaintext and served
a certificate issued by the deployment CA, verified against the root the
CA-root route serves. A bundled executor brought up under the same setting
proved mutual on its own listener across three handshake classes: a client
presenting no certificate was refused, a client presenting a certificate from a
freshly generated impostor CA was refused, and a client presenting a leaf the
deployment itself issued completed the handshake and saw a deployment-signed
server certificate. That executor's configuration entry declares no transport
setting of its own, yet the stack reported it as required — the change defaulted
it — and a node dispatched over that leg settled fresh. The untouched stack, with
no CA and no certificates, answered plaintext, reported its peer at transport
off, and drove a node to terminal unchanged. The universal was demonstrated on
two connection kinds — the control-API listener and a bundled executor leg — not
enumerated over every peer kind a deployment can configure; the store side is
covered by the transport-setting story's own runs.

## Compliance

The body names the delivery surface and its literal values — "I set `peer_auth: mtls`", "the default `none`" — which belong to a decision, not the story; compliant text says the operator turns on mutual authentication for internal traffic with one configuration change, and deployments that have not turned it on keep working unconfigured.
The body names a third-party testing tool ("my local dev and testcontainer stacks"); compliant text names the deployments in the product's own terms, e.g. "deployments I have not hardened".
