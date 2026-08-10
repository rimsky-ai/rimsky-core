---
audit: sensor-webhook
artifact: story:sensor-webhook
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# An external caller triggers a node by POSTing to an authenticated route

Supported. Two webhook subscriptions mounted live on one instance, one
authenticated by a shared header and one by an HMAC signature over a timestamp
and the body. Both authentication modes were exercised from outside the
orchestrator's network against the sensor's published listener: an authenticated
POST returned 200 and the message was already on the target instance when the
call returned, so no poll interval sits between the call and the message, and the
subscribed node ran. All five refusals the routes owe were taken: no credential,
wrong credential, wrong signature, a correct signature timestamped outside the
declared replay window, and a path no subscription declared, answered 401, 401,
401, 401 and 404, and neither refused POST on the shared-header route became a
message. A redelivery carrying a delivery id already seen returned 200 and
produced no second message. The sensor's health route answered before any
subscription existed. Every message the instance received came from the sensor.
