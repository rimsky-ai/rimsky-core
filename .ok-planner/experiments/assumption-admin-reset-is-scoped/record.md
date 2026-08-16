---
experiment: assumption-admin-reset-is-scoped
commit: d977250c
---

# What `rimsky admin reset` targets, and whether it asks

## What it ran against

One `rimsky-all-in-one` container from this tree's image set seeded with a
template, a tag, an instance, and two nodes. The run asks the verb what it
accepts as a target — no argument, two arguments, an instance id, a node id —
then runs it on a real pty with `n` waiting on stdin, so a confirmation prompt
would be seen and refused, and counts the whole deployment before and after.

## What was observed

The verb is scoped, more tightly than the prior supposes. It demands exactly
one argument (`usage: rimsky admin reset <node-id>`, exit 2 for none and for
two), and that argument is a node id and only a node id: passing an instance
id returns `404 node not found`. It is not a whole-deployment wipe — the
template, instance, and tag counts were identical before and after — and the
control API narrows it further, refusing with `409 reset only valid when node
has a failed terminal run in some scope`.

It asks nothing. On a pty with `n` on stdin the verb went straight to `POST
/v1/nodes/{id}/reset`; the `n` was never read. 2 checks, 1 pass, 1 fail.
