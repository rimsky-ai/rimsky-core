---
audit: peer-auth-mtls-mutual
artifact: story:peer-auth-mtls-mutual
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T05:45:00Z
---

# One flip authenticates the internal plane, and off costs nothing

Supported. With the single peer-auth setting changed and the CA key supplied,
the control-API listener refused plaintext and served a certificate issued by the
deployment CA, and a bundled executor brought up under the same setting proved
mutual: a caller with no certificate was refused at the handshake, a caller
holding a certificate from a freshly generated foreign CA was refused, and a
caller holding a leaf this deployment issued completed the handshake against a
deployment-signed server certificate. The executor's entry declares no transport
setting of its own, yet the stack reported it as requiring TLS and drove a node
through that leg to success. The default costs nothing: the same image with no
setting, no CA and no certificates answered plaintext, reported its peer at TLS
off, and drove a node to terminal unchanged.

## Compliance

The body prescribes mechanism in three places: it names a literal configuration
key and its two values, it names the certificate scheme that implements the
guarantee, and it names a third-party testing library. A story owes the need, not
the setting that satisfies it. Compliant text: "As an operator hardening a
production deployment, I can turn on authentication for internal
service-to-service traffic with one setting, and every internal connection then
refuses a peer that cannot prove it belongs to this deployment — while my local
development stacks keep working unchanged with the default off, so that I get an
authenticated internal plane without touching each service and pay nothing for it
when I don't need it."
