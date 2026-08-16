---
assumption: migrate-is-standalone-and-reversible
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `rimsky-migrate` can be run as a standalone one-shot job, is safe to re-run, and offers a down/rollback path.

As operator upgrading, I would take it that `rimsky-migrate` can be run as a standalone one-shot job, is safe to re-run, and offers a down/rollback path.

## Source

ecosystem-prior — a dedicated migrate binary in a platform with numbered migrations

## What a run would observe

run `rimsky-migrate` twice against a migrated database and look for a rollback subcommand or flag.

## Measured

Experiment `assumption-migrate-is-standalone-and-reversible` (seven checks, none
failing) drove the `rimsky-all-in-one` image and the CLI binary at this tree.
The prior holds in one part of three.

Re-running is safe. A second container booted over the same state directory
applied no migrations and came up healthy, where the first applied twenty-nine.

A one-shot run exists, but not by the route the prior expects. The image's
entrypoint reads its argument as a role name, and `rimsky-migrate` is not one:
`docker run … rimsky-migrate` exits 2 with `unknown role "rimsky-migrate"; valid
roles: rimsky-scheduler, rimsky-supervisor, rimsky-control-api`. The migrate
binary still ships in the image and still exits when it finishes, so a
deployment reaches it by replacing the image's entrypoint — the ordinary way a
container platform names a different command for an init container. What the
entrypoint itself offers is narrower: `RIMSKY_ENTRYPOINT_MIGRATE=1` forces the
step and then serves a role behind it, so that setting alone never produces a
container that finishes. The `0` setting is the one that serves a dedicated init
step, by taking the schema step out of the role containers' hands.

Reversible it is not. `RIMSKY_ENTRYPOINT_MIGRATE=down` is refused with `must be
"1" (force migrate), "0" (skip migrate), or unset`; `rimsky migrate` is an
unknown command; `migrate` appears nowhere in the CLI's help; and each driver
applies its numbered migrations forward only, with no down direction anywhere in
the runner. An operator planning a rollback step in an upgrade runbook has
nothing to call.
