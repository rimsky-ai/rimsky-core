---
assessment: executor-protocol--discovery
subject: story:executor-protocol
way: discovery
release: d977250c
outcome: held
warrant: experiment:executor-protocol
---
# Having my own executor discovered from one declared endpoint

The executor measured here is a third-party service built for the run: its own Go module whose only rimsky requirement is `catalog:published-packages/github.com/rimsky-ai/rimsky-core/lib/protocols (Go module)`, run beside a `catalog:images/rimsky-all-in-one` stack whose only knowledge of it is one `catalog:config-keys/executors.<name>.endpoint`. At startup the stack reached the peer over `catalog:grpc-rpcs/ExecutorObservability.Capabilities` and carried its whole advertisement back: both declared error classes, both declared tags, and its expected-attributes schema. The advertisement is readable to an operator afterwards at `catalog:http-routes/GET /v1/observability/executors`. Nothing about the service was configured into the stack beyond that one endpoint, so a service author needs no rimsky-internal knowledge to be found. Twenty-two checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
