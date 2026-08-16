---
experiment: assumption-migrate-is-standalone-and-reversible
commit: PENDING
---

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
