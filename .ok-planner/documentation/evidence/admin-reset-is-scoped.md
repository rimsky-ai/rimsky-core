---
trap: admin-reset-is-scoped
release: d977250c
---
# Evidence set — `rimsky admin reset` is a scoped, targeted recovery action (reset one instance or one node) rather than a whole-deployment wipe, and it requires confirmation.

Source of the prior: name-promise — `rimsky admin reset` with no object in the verb name, beside `--force` and `--yes` flags

## What the audit ran and observed (assumption record)

`.ok-planner/experiments/assumption-admin-reset-is-scoped` — built for this
run — asked the verb what it accepts as a target against a seeded
`rimsky-all-in-one` from this tree's image set, then ran it on a real pty with
`n` waiting on stdin and counted the whole deployment before and after.

The scoping half holds, and is tighter than the prior supposes. The verb
demands exactly one argument — `usage: rimsky admin reset <node-id>`, exit 2
with none and with two — and that argument must be a node id: an instance id
comes back `404 node not found`, so "reset one instance" is not on offer.
Nothing beyond the target moved; template, instance, and tag counts were
identical before and after. The control API narrows it further still,
answering `409 reset only valid when node has a failed terminal run in some
scope`.

The confirmation half is contradicted. On a pty, with `n` sitting on stdin,
the verb asked nothing and went straight to `POST /v1/nodes/{id}/reset`. The
`--yes` flag it accepts as a common flag answers no question, and `--force` is
not defined on it at all. 2 checks, 1 pass, 1 fail.

## Experiment record (experiment:assumption-admin-reset-is-scoped)

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

Runnables: `src:.ok-planner/experiments/assumption-admin-reset-is-scoped/` at the stamped commit.
