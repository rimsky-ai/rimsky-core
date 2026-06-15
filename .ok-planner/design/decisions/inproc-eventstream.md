---
decision: inproc-eventstream
status: as-is
aliases: []
---

# Channel-backed in-process event stream

## Choice

Channel-backed in-process event stream implementing the runtime's event-stream interface. The in-process client's execute method starts the handler on a goroutine that writes events to a buffered channel; the receive call reads from the channel; channel close signals stream end (returns end-of-stream). The handler's return error is surfaced through the stream per existing gRPC parity.

## Rationale

Matches the gRPC streaming semantics the dispatch loop is built around. The supervisor's read loop doesn't have to distinguish transports. Sync utility executors (counter, loop) emit all their events synchronously and return — the channel drains quickly. Async-style ones emit events as they happen.

## Alternatives

Synchronous event stream returning queued events from a slice. Avoids a goroutine but blocks the supervisor's read while the handler runs — incompatible with the streaming pattern the dispatch code assumes.
