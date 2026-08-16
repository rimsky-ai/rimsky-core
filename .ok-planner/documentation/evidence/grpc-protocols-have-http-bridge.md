---
trap: grpc-protocols-have-http-bridge
release: d977250c
---
# Evidence set — every protocol is reachable over an HTTP+JSON bridge as well as gRPC, since the config carries a `transport` selector and the bundled producers expose HTTP bridge ports.

Source of the prior: sibling-symmetry — `executors.<name>.transport`, `claim-producer-*: http_bridge_url`, and HTTP bridge ports 9110/9111

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-grpc-protocols-have-http-bridge)

# Asking which protocols speak HTTP+JSON

## What it ran against

The shipped `rimsky` CLI, a `rimsky-claim-producer-filesystem` container
publishing its HTTP bridge on port 9110, a `rimsky-executor-http-node`
container in stub mode serving gRPC on 9091 and its HTTP bridge on a
non-default 9099, and three `rimsky-all-in-one` stacks: one dispatching to that
executor over `transport: http`, one pointing `observability_endpoint` at the
same HTTP bridge, and one pointing a claim producer at its HTTP bridge.

## What was observed

Two protocols speak HTTP+JSON. `rimsky conformance claim-producer --transport
http` ran all 17 checks against the producer's bridge and exited 0, terminal
verbs included. The executor's bridge answered `POST /v1/Execute` with 200 and
a JSON Outcome, and `GET /observability/v1/capabilities` with 200.

Rimsky dispatches over that executor bridge. The stack accepted the executor
declared `transport: http` with endpoint `http://executor:9099`, registered and
deployed a template naming it, and the node settled `fresh` — the dispatch went
to port 9099, which carries only the HTTP bridge.

Rimsky reads everything else over gRPC. With `observability_endpoint` set to
`http://executor:9099` the stack logged
`observability.handshake.executor.unreachable` with
`http2: frame too large, note that the frame header looked like an HTTP/1.1
header`, and template registration then failed with "expected_attributes_schema
is not visible at registration". A claim producer whose endpoint is
`http://producer:9110` stopped the stack outright:
`dialRemoteClaimProducers: claim_producer "files": endpoint scheme must be
grpc:// (got http://)`.

The CLI refuses an HTTP transport for the rest. `rimsky conformance` answered
`--transport "http" not supported; use grpc` and exited 2 for `executor`,
`validation`, `data-processing`, `lifecycle-subscriber` and `publisher`, and
answered `--check-observability requires --transport grpc
(ClaimProducerObservability has no HTTP+JSON bridge)` for the producer's
observability sibling.

Runnables: `src:.ok-planner/experiments/assumption-grpc-protocols-have-http-bridge/` at the stamped commit.
