---
assumption: grpc-protocols-have-http-bridge
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every protocol is reachable over an HTTP+JSON bridge as well as gRPC, since the config carries a `transport` selector and the bundled producers expose HTTP bridge ports.

As service author in a language with weak gRPC support, I would take it that every protocol is reachable over an HTTP+JSON bridge as well as gRPC, since the config carries a `transport` selector and the bundled producers expose HTTP bridge ports.

## Source

sibling-symmetry — `executors.<name>.transport`, `claim-producer-*: http_bridge_url`, and HTTP bridge ports 9110/9111

## What a run would observe

run `rimsky conformance executor --transport http` and point a `claim_producers.<name>.endpoint` at the HTTP bridge.

## Measured

The experiment `assumption-grpc-protocols-have-http-bridge` drove each protocol
over HTTP+JSON where one exists and asked the CLI for it where one may not.
Two protocols speak HTTP+JSON: `rimsky conformance claim-producer --transport
http` ran all 17 checks against the bundled producer's bridge and exited 0, and
the bundled executor's bridge answered `POST /v1/Execute` with a JSON Outcome.
Rimsky itself dispatches over the executor bridge — a stack declaring
`transport: http` with endpoint `http://executor:9099` drove a node to `fresh`
over a port that carries only the bridge. Everything else is gRPC only. The CLI
answered `--transport "http" not supported; use grpc` and exited 2 for
`executor`, `validation`, `data-processing`, `lifecycle-subscriber` and
`publisher`, and said `ClaimProducerObservability has no HTTP+JSON bridge`. A
claim producer pointed at its own HTTP bridge stopped the stack with
`endpoint scheme must be grpc:// (got http://)`, and an
`observability_endpoint` pointed at the executor's HTTP bridge failed the gRPC
dial with `http2: frame too large`, after which the executor's attribute schema
never became visible and template registration failed. So the `transport`
selector the prior reasons from exists for one protocol, not for all: a service
author who plans to implement Validation, DataProcessing, Publisher,
LifecycleSubscriber or HostAgent over HTTP+JSON has no path at all, and one who
implements ExecutorObservability's bridge finds rimsky never dials it.
