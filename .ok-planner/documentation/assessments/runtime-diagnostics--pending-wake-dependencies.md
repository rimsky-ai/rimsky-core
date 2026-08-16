---
assessment: runtime-diagnostics--pending-wake-dependencies
subject: story:runtime-diagnostics
way: pending-wake-dependencies
release: d977250c
outcome: held
warrant: experiment:runtime-diagnostics
---
# Reading what the pending wakes are waiting for

Read through `catalog:http-routes/GET /v1/admin/diagnostics/wait-sets`, the wedged frame's wait set carried three edges, and every one named a sender run, a receiver run, and what it is waiting for — so an operator reads the dependency itself rather than inferring it. The reading was cross-checked against the rest of the picture: the receiver named on those edges had not run at all, which is consistent with the parked node upstream of it. Asked without a frame, the route refused with a client error rather than guessing which frame was meant.

## Unverified remainder

One wait set of three edges was read on one wedged instance. The way does not establish the answer's shape for an instance with many frames waiting at once.
