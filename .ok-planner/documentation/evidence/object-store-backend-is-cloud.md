---
trap: object-store-backend-is-cloud
release: d977250c
---
# Evidence set — `object-store: backend` names a cloud object store — S3, GCS, Azure Blob — not only a local filesystem path.

Source of the prior: name-promise — the key name `backend` under a publisher kind called `object-store`, plus `bucket` and `prefix` siblings

## What the audit ran and observed (assumption record)

Experiment `assumption-object-store-backend-is-cloud` (seven checks, none
failing) declared four object-store publishers differing only in `backend` —
`s3`, `gcs`, `azure`, `filesystem` — against a `rimsky-sensor-object-store`
container at this tree's tag. The prior does not hold. The build services two
backends and both are local: a sensor given only its environment root registers
`filesystem`, and one with every backend switch turned on registers exactly
`filesystem` and `memory` — a directory and an in-process map. Each of the three
cloud publishers stayed in `mounting`, never active, with the sensor's refusal in
the control API's log: `resolved_config.backend "s3" is not serviceable by this
build (registered backends: filesystem)`, and likewise for `gcs` and `azure`.
The `filesystem` publisher mounted live and a file written under the mounted
root arrived as a message, so `bucket` is a subdirectory of the operator's own
root rather than a cloud bucket. An operator wiring a data drop against S3 gets
a subscription that never mounts, and learns the reason only from the
orchestrator's log.

## Experiment record (experiment:assumption-object-store-backend-is-cloud)

# What `object-store: backend` can name

## What it ran against

A private docker network carrying a `rimsky-all-in-one` orchestrator, a
`rimsky-sensor-object-store` container with a host directory mounted as its
environment root, and a second sensor container with every backend switch the
image reads turned on. One template declares four object-store publishers
against the same sensor, differing only in the `backend` value: `s3`, `gcs`,
`azure`, and `filesystem`, each with a `bucket` and a `prefix`.

## What was observed

Seven checks, none failing.

The sensor registers one backend from its environment root: `filesystem`. With
`RIMSKY_SENSOR_OBJECT_STORE_ENABLE_MEMORY_BACKEND` also set, the second
container registered exactly two — `filesystem` and `memory`, a local directory
and an in-process map. There is no third.

The three cloud publishers never mounted. Each stayed in `mounting` and the
control API's log carries the sensor's refusal, `resolved_config.backend "s3" is
not serviceable by this build (registered backends: filesystem)`, and the same
for `gcs` and `azure` — the message names what the build can service, which is
the local directory.

The `filesystem` publisher mounted live, and a file written under the mounted
root arrived as a message naming `backend: filesystem`, `bucket: inbox` and
`object_name: in/alpha.txt`. The `bucket` is a subdirectory of the operator's
own root, not a cloud bucket.

Runnables: `src:.ok-planner/experiments/assumption-object-store-backend-is-cloud/` at the stamped commit.
