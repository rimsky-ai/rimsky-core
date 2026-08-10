---
experiment: peer-auth-mtls-mutual
commit: PENDING
---

# One flip, an authenticated internal plane

## What it ran against

Two stacks from the tree's own image tag. The first is `rimsky-all-in-one` with
one config key changed — `peer_auth: mtls` — plus the CA encryption key in its
environment, and a bundled `rimsky-executor-http-node` brought up with
`RIMSKY_PEER_AUTH=mtls` and the service key. The second is the same image on the
default `none` with a plaintext peer. Handshakes are probed from the host with
openssl; the deployment leaf used as a client certificate comes from the ruled
enrollment route.

## What was observed

After the flip the control-API listener answered plaintext HTTP with 400 and
served a certificate issued by `CN=rimsky-deployment-ca`, verified against the
root the CA-root route serves.

The bundled executor came up under the same flag and its listener proved mutual:
a client presenting no certificate was refused at the handshake, a client
presenting a certificate signed by a freshly generated impostor CA was refused,
and a client presenting a leaf the deployment issued completed the handshake and
saw a deployment-signed server certificate. The executor's config entry declares
no `tls:` key, yet the stack reported it at `tls: required` — the flip defaulted
it — and a node dispatched over that leg settled fresh.

The default costs nothing: the same image with no `peer_auth`, no CA and no
certificates answered plaintext, reported its peer reachable at `tls: off`, and
drove a node to terminal unchanged.
