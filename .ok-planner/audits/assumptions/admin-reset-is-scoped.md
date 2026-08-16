---
assumption: admin-reset-is-scoped
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `rimsky admin reset` is a scoped, targeted recovery action (reset one instance or one node) rather than a whole-deployment wipe, and it requires confirmation.

As operator recovering a stuck deployment, I would take it that `rimsky admin reset` is a scoped, targeted recovery action (reset one instance or one node) rather than a whole-deployment wipe, and it requires confirmation.

## Source

name-promise — `rimsky admin reset` with no object in the verb name, beside `--force` and `--yes` flags

## What a run would observe

run `rimsky admin reset --help` and see what it targets and whether it demands a target argument.

## Measured

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
