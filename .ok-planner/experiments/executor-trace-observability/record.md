---
experiment: executor-trace-observability
commit: d977250c
---

# A dashboard watches a dispatch live and reads it back afterwards

## What it ran against

A `rimsky-all-in-one` stack from the tree's own image tag with the bundled
`rimsky-executor-http-node` service wired in as an ordinary gRPC executor.
`client/` stands in for the operator's dashboard: a standalone Go module whose
only rimsky requirement is the protocols module, speaking the
executor-observability protocol directly, rebuilt by the run. `slowserver/` is
an HTTP endpoint that holds a request open until the run releases it, so the
dispatch is provably in flight rather than presumed so; the executor's egress
allowlist is opened for the docker network's own subnet so it can reach it.

## What was observed

Twenty checks, none failing. The operator learned everything needed from the
control API: the executor's observability endpoint and its advertisement that it
supports both trace fetch and trace streaming — the same two flags the client
then read back over the protocol itself. The dispatch id came from the event
feed.

With the executor's HTTP call held open, the client opened a stream and the
first event arrived while the dispatch could not have finished: the executor's
`step_started`, with the fetched trace still marked incomplete, no terminal event
streamed, and the request still held. Releasing the endpoint let the same open
stream carry the rest, in order: `step_started`, `step_completed`,
`trace_complete`.

Fetched after the fact, the trace named the dispatch the feed had named, came
back complete and not evicted, and carried both events. The records are
structured rather than log lines: every one carried an event id, timestamp,
severity, category and message, one carried machine-readable attributes, and the
completion event named its parent. An unknown dispatch id read back as evicted
rather than erroring.
