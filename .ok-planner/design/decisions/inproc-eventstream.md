---
decision: inproc-eventstream
status: as-is
aliases: []
---

# Unary in-process executor call

## Choice

The in-process executor client is a synchronous, unary call into the runtime executor package's handler interface. The client's execute method invokes the resolved in-process handler directly on the caller's goroutine and returns the resulting Outcome (or an error) once the handler completes — no goroutine handoff, no channel, no receive loop, and no end-of-stream signal.

## Rationale

Matches `decision:executor-unary-rpc`: the executor protocol's Execute call is unary, so the in-process transport mirrors the gRPC and HTTP-bridge transports exactly, with no transport-specific casing at the dispatch call site. Sync utility executors (counter, loop) and async-style ones alike simply return their one Outcome; there is nothing to stream.

## Alternatives

A goroutine-plus-channel event stream mirroring a server-streaming Execute call — rejected: the executor protocol itself is unary, so a streaming in-process transport would need to fabricate a receive loop the other two transports don't have, reintroducing exactly the concurrency and error-surfacing complexity the move to a unary protocol was meant to remove.
