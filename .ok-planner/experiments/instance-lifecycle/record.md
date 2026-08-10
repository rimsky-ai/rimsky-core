---
experiment: instance-lifecycle
commit: PENDING
---

# Instance runtime lifecycle through the CLI and control API

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image, running a
one-node template against the in-process `verifier-shape-checks` executor. The
probe drives `rimsky instance` verbs plus the public pause, resume, terminate
and message routes. `run.sh` boots and removes the container.

## What was observed

`instance create` returned an instance id and the instance's node graph was
materialized. After a message was posted, the event log reported
`work_completed`, `instance nodes` reported the node fresh, and `instance
status` reported its settling signal — progress readable at each step. On a
second instance, pause reported and read back as paused, a message posted
while paused stayed undelivered and no work ran; resume reported resumed and
the held work then ran. The terminate route stamped `terminated_at` on the
first instance and `instance kill --force` did the same on the second. After
`instance delete`, neither instance appeared in `instance list`.
