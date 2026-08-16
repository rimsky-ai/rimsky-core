---
assessment: publisher-protocol--release-on-terminate
subject: story:publisher-protocol
way: release-on-terminate
release: d977250c
outcome: held
warrant: experiment:publisher-protocol
---
# Terminating the instance releases the subscription exactly once

Terminating the instance through `catalog:http-routes/POST /v1/instances/{idOrKey}/terminate` released the subscription: the publisher was asked to unsubscribe exactly once and was left holding none. Subscriptions therefore have a bounded life tied to the instance that caused them, so a publisher's own state does not accumulate as workflows come and go.

## Unverified remainder

One instance was terminated, holding one subscription. The way does not establish release behaviour when an instance ends by failing rather than by an operator terminating it.
