---
assessment: single-process-all-in-one--one-process
subject: story:single-process-all-in-one
way: one-process
release: d977250c
outcome: held
warrant: experiment:single-process-all-in-one
---
# The all-in-one deployment is one process carrying all three roles

The audit read the running container's process table for `catalog:images/rimsky-all-in-one` on its baked defaults. It held exactly one rimsky process — the multi-role entrypoint — with no per-role child beside it, and it was still one after the deployment had done work. All three roles were serving out of that one process: the control API answered `catalog:http-routes/GET /v1/health`, one supervisor was registered, and a node dispatched and settled fresh. An operator running the all-in-one image therefore runs one container and one process, not three cooperating ones.

## Unverified remainder

None: the passing run demonstrates the way as promised.
