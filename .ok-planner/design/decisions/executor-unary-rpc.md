---
decision: executor-unary-rpc
---

# Execute is unary

## Choice

`rpc Execute(ExecuteRequest) returns (Outcome)` — unary. `Outcome` is a oneof of `Success | Error | Park | AwaitAsyncCallback`.

## Rationale

Nothing in a dispatch actually streams: a streamed shape would carry at most a weak liveness heartbeat ahead of the one terminal verdict. Unary is honest about the dispatch pattern and removes the stream-reader code path on both sides.

## Alternatives

- Server-streaming carrying only the single terminal message — rejected: preserves protocol complexity without any payoff.
- A heartbeat-bearing stream as an in-band liveness signal — rejected: a weak liveness signal doesn't justify a streaming surface on every executor.
