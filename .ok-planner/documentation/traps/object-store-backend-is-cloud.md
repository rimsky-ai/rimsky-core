---
trap: object-store-backend-is-cloud
release: d977250c
demonstration: experiment:assumption-object-store-backend-is-cloud
---
## Assumption

As operator wiring a data drop, I would take it that `object-store: backend` names a cloud object store — S3, GCS, Azure Blob — not only a local filesystem path.

name-promise — the key name `backend` under a publisher kind called `object-store`, plus `bucket` and `prefix` siblings

## Actual behavior

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
