---
trap: compose-restart-supervises
release: d977250c
---
# Evidence set — `instances[].restart` means the compose runtime re-creates or restarts an instance when it terminates, the way a container restart policy does.

Source of the prior: name-promise — a manifest key named `restart` in a docker-compose-shaped manifest

## What the audit ran and observed (assumption record)

`.ok-planner/experiments/assumption-compose-restart-supervises` — built for
this run — brought up two compose projects against one `rimsky-all-in-one`
from this tree's image set, one declaring `restart: always` and one `restart:
never`, force-terminated each instance, and then polled the instance listing
without running anything else.

Nothing re-created either instance. Under `restart: always` the live count
went from 1 to 0 across 30 polls and stayed there, while `compose status`
continued to report the instance as `in-manifest` with no indication it had
terminated — the operator's status view looks the same whether the instance is
running or dead.

The key is real, but it is read only by the next hand-run `compose up`. At
that point `compose plan` under `always` showed `- instance-delete
compose:restart-always:one (restart=always)` followed by `+ create …`, and
applying it restored the instance; under `never` the same situation planned
`no changes`. So `restart` is a classification rule for what a future apply
does with an already-terminated instance, not a supervision policy. Between
applies there is no compose runtime at all — `compose up` is a one-shot
reconciler that exits, and nothing outlives it to notice a termination.
2 checks, 0 pass, 2 fail.

## Experiment record (experiment:assumption-compose-restart-supervises)

# What `instances[].restart` supervises

## What it ran against

One `rimsky-all-in-one` container from this tree's image set and two compose
projects, one declaring `restart: always` and one `restart: never` as the
control. Each is brought up, its instance is force-terminated, and the run
then polls the instance listing for a re-creation without running anything —
if a compose runtime supervises the instance, it has an idle deployment and a
terminal instance to act on. Afterwards the run invokes `compose plan` and
`compose up` by hand to establish what the policy does do.

## What was observed

Nothing supervises. Under `restart: always` the instance stayed terminated
across 30 polls, live instances went from 1 to 0 and stayed there, and
`compose status` kept reporting the instance as `in-manifest` with no sign it
had died. The control behaved the same way.

The policy is real but is only read by the next hand-run `compose up`. At that
point `compose plan` under `always` showed two changes — `instance-delete
compose:restart-always:one (restart=always)` then `create
compose:restart-always:one` — and applying them brought the count back to 1.
Under `never` the same plan said `no changes`, leaving the terminal instance
in place. So `restart` classifies what a future apply does with an instance
that has already terminated; it is not a restart policy in the container
sense, and between applies nothing is watching. 2 checks, 0 pass, 2 fail.

Runnables: `src:.ok-planner/experiments/assumption-compose-restart-supervises/` at the stamped commit.
