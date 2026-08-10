---
audit: validation-mixin-uniform
artifact: story:validation-mixin-uniform
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# The validation mix-in carried by an executor peer and a publisher peer

Supported. Three purpose-built services, each advertising the mix-in through
its own primary protocol's capabilities handshake, were declared to a
zero-config all-in-one deployment, and one template naming all three settled
it. The executor peer's validator was called for the executor role with the
node's alias; the publisher peer's validator was called for the publisher role
with the publisher's name; neither peer is a claim producer. The third peer
advertised the mix-in but declared a role it plays no part in, and was never
called, so the declared roles are honoured rather than every mix-in being
called for everything. Both findings came back on registration as well as on
validation. Of the 4 roles the protocol defines, 2 were driven here;
claim-producer and lifecycle-subscriber were not.

## Compliance

Two defects. The benefit clause — "so the mix-in works for every peer kind the
protocol says it does" — restates the capability and appeals to the protocol's
own consistency instead of naming what the service author accomplishes. The
body also frames a change rather than a durable expectation: "not only from a
claim producer" and "actually honored" describe a gap being closed, and a story
describes the project as it stands. Compliant text: "As a service author, I
want my validation rules consulted whatever kind of service I attach them to,
and only for the roles I declare, so that I validate the same way for every
service I run."
