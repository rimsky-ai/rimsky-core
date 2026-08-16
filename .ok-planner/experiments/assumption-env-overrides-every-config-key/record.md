---
experiment: assumption-env-overrides-every-config-key
commit: d977250c
---

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
