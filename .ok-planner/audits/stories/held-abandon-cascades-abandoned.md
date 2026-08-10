---
audit: held-abandon-cascades-abandoned
artifact: story:held-abandon-cascades-abandoned
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# The abandoned-error signal fires at the moment held work rolls back

Supported. Against an all-in-one deployment with a filesystem-backed claim
producer, one node opened a claim, a co-holder of that claim failed its work,
and the claim rolled back with a single abandon. The acquirer emitted exactly
one terminal signal, the abandoned-error one. Both of the subscription forms the
story names were declared on non-member downstream nodes and both fired: the one
naming the abandoned-error signal exactly and the one naming the broader
error-family pattern each ran once, each starting after the abandon in the event
log. A third downstream node subscribed to success never ran, so the rollback
was never reported as a success.
