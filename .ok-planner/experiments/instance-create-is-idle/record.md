---
experiment: instance-create-is-idle
commit: PENDING
---

# Creating an instance has no side effect

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image, running a
one-node template against the in-process `verifier-shape-checks` executor. The
negative is anchored to a second instance rather than to elapsed time: a
sibling instance is woken and driven to completion, and only then is the
untouched instance re-read. `run.sh` boots and removes the container.

## What was observed

`instance create` materialized the node graph, but every node run counter was
zero, the instance's event log was empty, and no message was enqueued. A
second instance was created and woken, and ran its node to completion — so the
scheduler was demonstrably running. Re-read at that point, the untouched
instance still had no events and still had zero run counters. Posting a
message to it then drove it to completion, so invoking work is the separate
operator action.
