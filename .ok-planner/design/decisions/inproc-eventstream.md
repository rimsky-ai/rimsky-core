---
decision: inproc-eventstream
---

# Unary in-process executor call

## Choice

The in-process executor client is a unary call into the runtime executor
package's handler interface: one invocation, one Outcome (or error)
returned to the caller, with no event stream, no receive loop, and no
end-of-stream signal. The invocation is bridged through a goroutine and a
buffered result channel for exactly two purposes — so the caller's context
cancellation is honored even when a handler blocks, and so a handler panic
is recovered rather than taking down the process that hosts every other
role alongside it.

## Rationale

Matches `decision:executor-unary-rpc`: the executor protocol's Execute call
is unary, so the in-process transport mirrors the gRPC and HTTP-bridge
transports exactly, with no transport-specific casing at the dispatch call
site. Sync utility executors (counter, loop) and async-style ones alike
simply return their one Outcome; there is nothing to stream.

The goroutine bridge is isolation machinery rather than transport shape.
In-process execution shares an address space with the scheduler,
supervisor, and control API, so an unrecovered handler panic or an
uncancellable blocking handler would take all of them down with it — a cost
the out-of-process transports do not carry, and the reason this one
transport pays for a bridge the others have no use for.

## Alternatives

- A goroutine-plus-channel event stream mirroring a server-streaming
  Execute call — rejected: the executor protocol itself is unary, so a
  streaming in-process transport would need to fabricate a receive loop the
  other two transports don't have, reintroducing exactly the concurrency
  and error-surfacing complexity a unary protocol avoids.
- A bare synchronous call on the caller's goroutine with no bridge at all —
  rejected: it is simpler, but it gives up both cancellation of a blocking
  handler and panic isolation, which in a single-process deployment means
  one misbehaving executor can hang or crash every role at once.
