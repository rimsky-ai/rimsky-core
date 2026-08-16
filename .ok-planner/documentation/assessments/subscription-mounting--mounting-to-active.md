---
assessment: subscription-mounting--mounting-to-active
subject: story:subscription-mounting
way: mounting-to-active
release: d977250c
outcome: held
warrant: experiment:subscription-mounting
---
# Watching a publisher subscription go from mounting to active

The audit created an instance from a template with one publisher entry and read its subscriptions back through `catalog:http-routes/GET /v1/instances/{idOrKey}`. The instance exposed one subscription per declared publisher entry, carrying the publisher's name, its kind and its message type; the operator saw that subscription in the mounting state and then in the active state, and in no other state. Active meant what the story says it means: a message attributed to that publisher then arrived and the node the template wired to its type ran. The operator therefore knows from the instance itself when the sensor is actually feeding it.

## Unverified remainder

One publisher entry on one instance was exercised. The demonstration does not establish what an operator sees when a subscription that was active later stops being fed.
