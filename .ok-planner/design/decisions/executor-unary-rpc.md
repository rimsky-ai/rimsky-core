---
decision: executor-unary-rpc
status: as-is
aliases: []
---

# Execute becomes unary

## Choice

`rpc Execute(ExecuteRequest) returns (Outcome)` — unary. `Outcome` is a oneof of `Success | Error | Park | AwaitAsyncCallback`.

## Rationale

The historical stream carried only Heartbeats (a weak liveness signal) and the eventual terminal verdict. With NamedEvent collapsed into terminal tags and Heartbeat removed, nothing actually streams. Unary is honest about the dispatch pattern and removes the stream-reader code path on both sides.

## Alternatives

Keep server-streaming with NamedEvent / Heartbeat removed — rejected because it preserves protocol complexity without any payoff.
