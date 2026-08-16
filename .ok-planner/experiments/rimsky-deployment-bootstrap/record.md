---
experiment: rimsky-deployment-bootstrap
commit: d977250c
---

# Role selection and migration ownership in the bundled entrypoint

The story makes two claims — the operator picks the topology, and migrations run
exactly once per deployment — so this directory holds two runnable ways. Both
drive the `rimsky` image at `RIMSKY_IMAGE_TAG` with a mounted deployment config
and a mounted supervisor config.

## way-role-selection.py

### What it ran against

A docker network carrying a postgres container, plus `rimsky` containers started
with each legal command and each illegal one. The no-command case runs against a
SQLite config instead, so it owns its own database.

### What was observed

A container whose command named `rimsky-control-api` ran that role alone — its
process table held the entrypoint and one control-api child — completed 28
migrations, and answered on the control API. A container whose command named
`rimsky-scheduler` reported that role alone and logged that it was skipping
migrations; a container whose command named `rimsky-supervisor` did the same. A
container with no command reported all three roles, ran them in a single process,
and completed the migrations before serving.

Three illegal launches each exited 2 without starting anything: an unknown role
name and `rimsky-migrate` were both refused as unknown roles naming the three
valid ones, and two role arguments were refused for exceeding one.

Eleven checks, none failing.

## way-migrate-discipline.py

### What it ran against

A postgres container with three databases, and `rimsky` containers started
against them. The two extra databases are created by a loop that polls until the
database is actually listed, because the postgres image reports ready during its
init phase and a single unchecked create can be lost. The split deployment is
three containers — scheduler, supervisor and control-api — sharing one database,
each with a restart policy so a role that starts before the schema exists
retries.

### What was observed

Across the three-container split, exactly one container ran the migrations, and
it was the control-api container; the other two logged that they were skipping
them. The schema arrived: the deployment served, and a node dispatched and
settled at `terminal/success` across the split roles.

The override moved ownership in both directions. A no-command container with the
override set to 0 logged that it was skipping the migrations it would otherwise
have owned. A scheduler-only container with the override set to 1 ran them and
completed 28. A container with the override set to `yes` exited 2 at startup with
an error naming the variable, the value, and the two legal values.

Seven checks, none failing.

RESULT: PASS
