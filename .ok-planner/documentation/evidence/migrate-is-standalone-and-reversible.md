---
trap: migrate-is-standalone-and-reversible
release: d977250c
---
# Evidence set — `rimsky-migrate` can be run as a standalone one-shot job, is safe to re-run, and offers a down/rollback path.

Source of the prior: ecosystem-prior — a dedicated migrate binary in a platform with numbered migrations

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-migrate-is-standalone-and-reversible)

# Running the migrate step, twice, and looking for the way back

## What it ran against

The `rimsky-all-in-one` image at the tree's own tag and the CLI binary built
from the same tree. The run asks the image to run `rimsky-migrate` as its
command; boots a role container over a host state directory with
`RIMSKY_ENTRYPOINT_MIGRATE=1`; boots a second one over the same directory; then
looks for a way down — through the migrate switch, through the CLI's verbs, and
through the CLI's own help.

## What was observed

Seven checks, none failing.

The migrate binary is not a command the image will run. `docker run … rimsky-migrate`
exited 2 with `unknown role "rimsky-migrate"; valid roles: rimsky-scheduler,
rimsky-supervisor, rimsky-control-api`. Forcing the step with
`RIMSKY_ENTRYPOINT_MIGRATE=1` runs it — twenty-nine migrations applied — but
leaves the role serving behind it: the container is still running, so the step
is part of a long-running container's startup rather than a job that finishes.

Re-running is safe. The second container over the same state directory applied
nothing and came up healthy.

There is no way down. `RIMSKY_ENTRYPOINT_MIGRATE=down` is refused with `must be
"1" (force migrate), "0" (skip migrate), or unset`; `rimsky migrate` is an
unknown command; and the word `migrate` does not appear in the CLI's help at
all.

Runnables: `src:.ok-planner/experiments/assumption-migrate-is-standalone-and-reversible/` at the stamped commit.
