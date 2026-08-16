---
assessment: runtime-diagnostics--held-frames
subject: story:runtime-diagnostics
way: held-frames
release: d977250c
outcome: held
warrant: experiment:runtime-diagnostics
---
# Reading which frames the holding sub-graph is gripping

Read through `catalog:http-routes/GET /v1/admin/diagnostics/held-frames`, the roster reported one frame for the wedged instance, named the parked node holding it, reported its state as parked, and reported how long the frame had been held — which is the signal that separates a wedge from ordinary in-flight work. The answer was cross-checked rather than taken on its own: that same frame id appears on the instance's own frame listing at `catalog:http-routes/GET /v1/instances/{id}/frames`, so the diagnostic and the instance's ordinary view agree.

## Unverified remainder

One held frame on one instance was read. The way does not establish the roster under several concurrently held frames.
