---
trap: every-protocol-has-observability-sibling
release: d977250c
demonstration: experiment:assumption-every-protocol-has-observability-sibling
---
## Assumption

As operator building a dashboard, I would take it that each service protocol has a matching observability protocol, so publishers and lifecycle subscribers are inspectable the way executors and claim producers are.

sibling-symmetry — `ExecutorObservability` and `ClaimProducerObservability` with no `PublisherObservability` or `LifecycleSubscriberObservability`

## Actual behavior

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
