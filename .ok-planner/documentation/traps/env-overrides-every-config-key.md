---
trap: env-overrides-every-config-key
release: d977250c
demonstration: experiment:assumption-env-overrides-every-config-key
---
## Assumption

As operator deploying in Kubernetes, I would take it that every `rimsky.yml` key has a corresponding `RIMSKY_`-prefixed environment override, so a container can be configured without mounting a file.

sibling-symmetry — `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST` overrides `supervisor.yml: callback.advertise_host`, and `RIMSKY_CONTROL_API_HOST`/`PORT` shadow config

## Actual behavior

Experiment `assumption-env-overrides-every-config-key` (nine checks, none
failing) drove three `rimsky-all-in-one` containers at this tree's image tag.
The prior does not hold. Eleven variables named after the experiment keys the way
an operator would guess them changed nothing: a container told
`RIMSKY_PERSISTENCE_DRIVER=postgres`, with a DSN pointing at a dead port, came
up healthy on SQLite; the database opened at the baked
`persistence.sqlite.path` rather than at the path
`RIMSKY_PERSISTENCE_SQLITE_PATH` named; the executor and claim producer named
by `RIMSKY_EXECUTORS_FOO_ENDPOINT` and `RIMSKY_CLAIM_PRODUCERS_BAR_ENDPOINT`
were absent from `/v1/observability/executors` and
`/v1/observability/claim-producers`; and the startup log named none of the
eleven, so they are ignored silently rather than refused. The same two settings
declared in a mounted the experiment took effect, and a `${DB_PATH}`
reference inside that file did resolve from the container's environment — file
interpolation, not per-key override, is how environment values reach
the experiment. A fixed enumerated set of variables does configure a deployment:
`RIMSKY_CONTROL_API_PORT=8099` moved the control API's listener, and the probe
reached `/v1/health` there. An operator who configures a container by guessing
variable names gets a silently unchanged deployment.
