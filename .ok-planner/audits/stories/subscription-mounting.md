---
audit: subscription-mounting
artifact: story:subscription-mounting
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# A publisher subscription observed from mounting to active

Supported. Against an all-in-one deployment with a bundled cron sensor
registered as its one publisher, an instance whose template declares a publisher
entry exposed one subscription carrying that publisher's name, kind and message
type. The operator read it in the mounting state and then in the active state,
and in no other state. A message attributed to that publisher then arrived and
the node the template wired to its message type ran, so active names a sensor
that is feeding the instance. A second instance, whose template names a
publisher this deployment does not run, was created successfully while its
subscription reports failed with the reason that the publisher is not
registered — which is exactly the outcome the create response did not carry.
