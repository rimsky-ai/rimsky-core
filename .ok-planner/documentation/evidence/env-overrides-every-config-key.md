---
trap: env-overrides-every-config-key
release: d977250c
---
# Evidence set — every `rimsky.yml` key has a corresponding `RIMSKY_`-prefixed environment override, so a container can be configured without mounting a file.

Source of the prior: sibling-symmetry — `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST` overrides `supervisor.yml: callback.advertise_host`, and `RIMSKY_CONTROL_API_HOST`/`PORT` shadow config

## What the audit ran and observed (assumption record)

Experiment `assumption-env-overrides-every-config-key` (nine checks, none
failing) drove three `rimsky-all-in-one` containers at this tree's image tag.
The prior does not hold. Eleven variables named after `rimsky.yml` keys the way
an operator would guess them changed nothing: a container told
`RIMSKY_PERSISTENCE_DRIVER=postgres`, with a DSN pointing at a dead port, came
up healthy on SQLite; the database opened at the baked
`persistence.sqlite.path` rather than at the path
`RIMSKY_PERSISTENCE_SQLITE_PATH` named; the executor and claim producer named
by `RIMSKY_EXECUTORS_FOO_ENDPOINT` and `RIMSKY_CLAIM_PRODUCERS_BAR_ENDPOINT`
were absent from `/v1/observability/executors` and
`/v1/observability/claim-producers`; and the startup log named none of the
eleven, so they are ignored silently rather than refused. The same two settings
declared in a mounted `/etc/rimsky/rimsky.yml` took effect, and a `${DB_PATH}`
reference inside that file did resolve from the container's environment — file
interpolation, not per-key override, is how environment values reach
`rimsky.yml`. A fixed enumerated set of variables does configure a deployment:
`RIMSKY_CONTROL_API_PORT=8099` moved the control API's listener, and the probe
reached `/v1/health` there. An operator who configures a container by guessing
variable names gets a silently unchanged deployment.

## Experiment record (experiment:assumption-env-overrides-every-config-key)

# What a `RIMSKY_*` variable can and cannot configure

## What it ran against

Three `rimsky-all-in-one` containers from the tree's own image tag, each with
the state directory bind-mounted so the database file it actually opens is
visible from outside. The first container carries eleven variables named after
`rimsky.yml` keys the way an operator would guess them
(`RIMSKY_PERSISTENCE_DRIVER`, `RIMSKY_PERSISTENCE_SQLITE_PATH`,
`RIMSKY_EXECUTORS_FOO_ENDPOINT`, `RIMSKY_CLAIM_PRODUCERS_BAR_ENDPOINT`,
`RIMSKY_RETENTION_RECENT_FRAMES_KEPT` and the rest) and mounts no config file.
The second carries the same two settings inside a mounted
`/etc/rimsky/rimsky.yml`, one of them written as a `${DB_PATH}` reference. The
third sets `RIMSKY_CONTROL_API_PORT`, a variable the product does read.

## What was observed

Nine checks, none failing.

Not one of the eleven guessed variables changed anything. The container told
`RIMSKY_PERSISTENCE_DRIVER=postgres` with a DSN pointing at a port where nothing
listens came up healthy on SQLite — an honored override could not have. The
database file appeared at the image's baked `persistence.sqlite.path`
(`state.db`), not at the path `RIMSKY_PERSISTENCE_SQLITE_PATH` named, and no
blob root appeared where `RIMSKY_PERSISTENCE_BLOB_FILESYSTEM_ROOT` named one.
`GET /v1/observability/executors` listed only the three in-process bundled
executors, without the `foo` that `RIMSKY_EXECUTORS_FOO_ENDPOINT` named, and
`GET /v1/observability/claim-producers` came back empty despite
`RIMSKY_CLAIM_PRODUCERS_BAR_ENDPOINT`. The startup log named none of the eleven:
they are ignored in silence, not rejected.

The mounted file carried both settings that the environment could not. The same
`executors.foo` block declared in `/etc/rimsky/rimsky.yml` appeared in the
observability route, and the `${DB_PATH}` reference inside the file resolved
from the container's environment, so the database opened at `from-file.db`.
Environment values reach `rimsky.yml` through that interpolation, not through
per-key overrides.

`RIMSKY_CONTROL_API_PORT=8099` did move the control API's listener, which the
probe confirmed by reaching `/v1/health` there — so a fixed, enumerated set of
variables does configure a deployment, and the guessable per-key form is what
does not exist.

Runnables: `src:.ok-planner/experiments/assumption-env-overrides-every-config-key/` at the stamped commit.
