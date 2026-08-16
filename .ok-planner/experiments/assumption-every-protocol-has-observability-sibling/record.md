---
experiment: assumption-every-protocol-has-observability-sibling
commit: PENDING
---

# Looking for an observability sibling behind each peer class

## What it ran against

A `rimsky-all-in-one` stack whose `rimsky.yml` declares four peer classes and
gives every one of them an `observability_endpoint`: an executor
(`rimsky-executor-http-node`), a claim producer
(`rimsky-claim-producer-filesystem`), a publisher (`rimsky-sensor-cron`) and a
validator (`rimsky-executor-verifier-shape-checks`). The run reads the control
API's observability routes, then dials the observability sibling of each
protocol directly with a probe built against the published
`github.com/rimsky-ai/rimsky-core/lib/protocols` module.

## What was observed

The stack accepted `observability_endpoint` under `executors`,
`claim_producers`, `publishers` and `validators` alike and started healthy.

Two peer classes are readable. `GET /v1/observability/executors/http` returned
the executor's `observability_capabilities` with `supports_trace_get: true`,
and `GET /v1/observability/claim-producers/files` returned
`supports_claim_get: true`.

The other classes have no read at all. `GET /v1/observability/publishers`,
`/subscribers`, `/lifecycle-subscribers`, `/validators` and
`/data-processors` each returned 404, and the publisher named `tick` in the
config appears nowhere in `GET /v1/observability/system/summary`, though the
stack accepted an observability endpoint for it.

The protocols match the routes. `ClaimProducerObservability.Capabilities`
answered at the producer's endpoint, while
`/rimsky.v1.PublisherObservability/Capabilities`,
`/rimsky.v1.LifecycleSubscriberObservability/Capabilities`,
`/rimsky.v1.ValidationObservability/Capabilities` and
`/rimsky.v1.DataProcessingObservability/Capabilities` each came back
`Unimplemented` with "unknown service" — the servers do not carry those
services, and the shipped proto set does not declare them.
