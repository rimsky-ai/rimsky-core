---
assessment: debug-channel--paused-gate
subject: story:debug-channel
way: paused-gate
release: d977250c
outcome: held
warrant: experiment:debug-channel
---
# Overriding a node and an attribute while the instance is paused

The audit drove a deployment of `catalog:images/rimsky-all-in-one` on an instance running with a node parked. Before the instance was paused, both override actions on `catalog:http-routes/POST /v1/instances/{id}/debug/override` were refused with a conflict naming the two states that would open the channel, and the node's attribute values were unchanged afterwards. Once the instance was paused through `catalog:http-routes/POST /v1/instances/{idOrKey}/pause`, setting an attribute answered with the paused gate state and one run mutated, and the node read back carrying the operator's value; invalidating a node answered on the same open channel. No work ran while the instance stayed paused, and resuming it ran the invalidated node again — so the operator inspects and mutates while everything is still, then lets the consequences play out. Nine checks ran in this way and none failed.

## Unverified remainder

None: the passing run demonstrates the way as promised.
