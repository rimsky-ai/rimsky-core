---
audit: validation-mixin-uniform
artifact: story:validation-mixin-uniform
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:58:36Z
---

# A peer's validation mix-in is consulted from an executor and from a publisher, for the roles it declares

Supported. Measured with a purpose-built service speaking only the published
protocols, run three times over — an executor peer declaring the executor role, a
second executor peer declaring only the claim-producer role, and a publisher peer
declaring the publisher role — against a released-image stack that knew them
only as three declared peers. Seven checks, none failing. Validating one template
that names both executor peers as node executors and the publisher peer as a
publisher returned two mix-in findings: one from the executor peer, called for
the executor role with the node it was called about, and one from the publisher
peer, called for the publisher role with the publisher name — neither of them a
claim producer, and both discovered through the peer's own capabilities
handshake. The peer that advertised the mix-in but declared a role it does not
play in the template was never called, so declared roles are what is honoured.
Registration returned the same two findings, so the mix-ins are consulted there
too and not only on validation.

## Compliance

- The benefit clause states the product's internal consistency rather than a user outcome — "so the mix-in works for every peer kind the protocol says it does"; the compliant benefit is that the author's service can vet the templates that use it whatever role it plays in them.
- The body carries change-record framing — "not only from a claim producer" and "actually honored" describe a correction rather than a durable expectation; the compliant text states the capability directly.
