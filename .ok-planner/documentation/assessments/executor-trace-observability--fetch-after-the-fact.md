---
assessment: executor-trace-observability--fetch-after-the-fact
subject: story:executor-trace-observability
way: fetch-after-the-fact
release: d977250c
outcome: held
warrant: experiment:executor-trace-observability
---
# Fetching the whole trace for a dispatch that has finished

Fetched after the dispatch settled through `catalog:grpc-rpcs/ExecutorObservability.GetTrace`, the trace named the dispatch that `catalog:http-routes/GET /v1/events` had named, came back complete and not evicted, and carried both of the events the live stream had delivered. The records are structured rather than log lines: every one carried an event id, timestamp, severity, category and message, one carried machine-readable attributes, and the completion event named its parent, so a dashboard can group and filter them rather than only display them. An unknown dispatch id read back as evicted rather than as an error, so a dashboard asking about work the deployment no longer retains gets a definite answer.

## Unverified remainder

None: the passing run demonstrates the way as promised.
