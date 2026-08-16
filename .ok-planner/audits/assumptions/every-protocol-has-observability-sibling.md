---
assumption: every-protocol-has-observability-sibling
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# each service protocol has a matching observability protocol, so publishers and lifecycle subscribers are inspectable the way executors and claim producers are.

As operator building a dashboard, I would take it that each service protocol has a matching observability protocol, so publishers and lifecycle subscribers are inspectable the way executors and claim producers are.

## Source

sibling-symmetry — `ExecutorObservability` and `ClaimProducerObservability` with no `PublisherObservability` or `LifecycleSubscriberObservability`

## What a run would observe

check the ten proto files and the `observability_endpoint` config keys for a publisher or subscriber observability surface.

## Measured

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
