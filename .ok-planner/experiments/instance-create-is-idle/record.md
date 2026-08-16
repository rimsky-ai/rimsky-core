---
experiment: instance-create-is-idle
commit: d977250c
---

# Creating an instance has no side effect

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image, running a
one-node template against the in-process `verifier-shape-checks` executor. The
probe drives the public CLI and the public instance-messages route. The
negative is anchored to a second instance rather than to elapsed time: a
sibling instance is woken and driven to completion, and only then is the
untouched instance re-read. `run.sh` boots and removes the container.

## What was observed

The whole probe passed at this tree. `instance create` returned an instance id
and materialized the node graph — the root node was listed — while every node
run counter read zero, the instance's event log was empty, and its message
queue was empty. A second instance was then created, woken by a posted
message, and driven to `work_completed`, proving the scheduler was running.
Re-read at that point, the untouched instance still had no events and still
had zero run counters. Posting a message to it then drove it to
`work_completed` as well, so invoking work is the separate operator action.
