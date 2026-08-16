---
assessment: executor-trace-observability--live-stream
subject: story:executor-trace-observability
way: live-stream
release: d977250c
outcome: held
warrant: experiment:executor-trace-observability
---
# Watching an executor's trace events arrive while the dispatch is still running

A standalone dashboard client — its own Go module depending only on `catalog:published-packages/github.com/rimsky-ai/rimsky-core/lib/protocols (Go module)` — learned the executor's observability endpoint and its support for both trace fetch and trace streaming from `catalog:http-routes/GET /v1/observability/executors`, and read the same two flags back over `catalog:grpc-rpcs/ExecutorObservability.Capabilities`. The dispatch id came from `catalog:http-routes/GET /v1/events`. With the upstream HTTP call the dispatch had made held open, so that the dispatch provably could not have finished, the client opened `catalog:grpc-rpcs/ExecutorObservability.StreamTrace` and the executor's first step event arrived: the fetched trace was still marked incomplete, no terminal event had streamed, and the request was still held. Releasing the upstream let the same open stream carry the remainder in order — step started, step completed, trace complete. Twenty checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
