---
trap: every-protocol-has-observability-sibling
release: d977250c
---
# Evidence set — each service protocol has a matching observability protocol, so publishers and lifecycle subscribers are inspectable the way executors and claim producers are.

Source of the prior: sibling-symmetry — `ExecutorObservability` and `ClaimProducerObservability` with no `PublisherObservability` or `LifecycleSubscriberObservability`

## What the audit ran and observed (assumption record)

The experiment `assumption-every-protocol-has-observability-sibling` wired a
stack whose executor, claim producer, publisher and validator each declare an
`observability_endpoint`, then read the control API and dialed each protocol's
observability sibling. The stack accepted the key under all four peer classes
and started healthy, so the config gives the operator no signal. Only two peer
classes are readable: `GET /v1/observability/executors/http` returned
`supports_trace_get: true` and `GET /v1/observability/claim-producers/files`
returned `supports_claim_get: true`. `GET /v1/observability/publishers`,
`/subscribers`, `/lifecycle-subscribers`, `/validators` and
`/data-processors` each returned 404, and the publisher named in the config
appears nowhere in `GET /v1/observability/system/summary`. The protocols match:
`ClaimProducerObservability.Capabilities` answered, while
`PublisherObservability`, `LifecycleSubscriberObservability`,
`ValidationObservability` and `DataProcessingObservability` each came back
`Unimplemented` with "unknown service" — the shipped proto set declares only
the two observability protocols. An operator who wires
`publishers.<name>.observability_endpoint` expecting a dashboard read gets a
configuration key that parses, starts, and does nothing.

## Experiment record (experiment:assumption-every-protocol-has-observability-sibling)

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

Runnables: `src:.ok-planner/experiments/assumption-every-protocol-has-observability-sibling/` at the stamped commit.
