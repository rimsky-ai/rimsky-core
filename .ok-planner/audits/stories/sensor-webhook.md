---
audit: sensor-webhook
artifact: story:sensor-webhook
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:35:00Z
---

# An external caller triggers a node by calling an authenticated endpoint, with no poll in between

Supported. Fourteen checks against a deployment carrying the bundled webhook
sensor, its listener published so the calls arrive from outside the network the
orchestrator runs on. The sensor answered its health route before any
subscription existed, and both declared subscriptions mounted live. An
authenticated call on the declared path returned success and was already a
message on the target instance when the call returned — no poll interval sits
between the two — and the subscribed node ran on it. Authentication was measured
in both declared forms: a call with no credential and a call with the wrong
credential were each refused, and neither became a message; a correctly signed
call was accepted and was likewise already a message, carrying its delivery id;
a call signed with the wrong secret was refused, and a correctly signed call
bearing a timestamp an hour old was refused by the declared replay window.
Redelivering a body under a delivery id already seen was accepted and produced
no second message, a call to a path no subscription declared was refused, and
every message the instance held came from the sensor.

## Compliance

The body names the delivery surface — "expose authenticated HTTP routes that translate inbound POSTs into messages" — and routes and wire verbs belong to a decision; compliant text says the operator exposes authenticated endpoints and each accepted call becomes a message on the subscription's target instance.
