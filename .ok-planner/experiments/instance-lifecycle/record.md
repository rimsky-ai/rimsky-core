---
experiment: instance-lifecycle
commit: d977250c
---

# Instance runtime lifecycle through the CLI and control API

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image, running a
one-node template against the in-process `verifier-shape-checks` executor. The
probe drives the `rimsky instance` verbs plus the public pause, resume,
terminate and instance-messages routes. `run.sh` boots and removes the
container.

## What was observed

The whole probe passed at this tree. `instance create` returned an instance id
and the instance's root node was materialized. After a message was posted, the
event log reported `work_completed`, `instance nodes` reported the node fresh,
and `instance status` reported its terminal success signal — progress readable
at each step. On a second instance, pause reported and read back as paused, a
message posted while paused was queued but never marked delivered and no work
ran; resume reported resumed and the held work then ran to `work_completed`.
The terminate route stamped `terminated_at` on the first instance, which read
back terminated, and `instance kill --force` did the same on the second. After
`instance delete` on both, neither appeared in `instance list`.
