---
decision: inproc-handler-interface
---

# What in-process utility executors implement

## Choice

In-process utility executors implement a small Go interface in the runtime executor package: one Execute method that takes a context, the executor's execute-request DTO, and a handler-context struct giving the handler its few legitimate side-channel effects (scratch access, cascade-message sending); the handler returns the terminal Outcome DTO directly, or an error the in-process client translates into an error terminal. Generated protobuf types are passed as DTOs at the function-call boundary — no wire encoding.

## Rationale

Shape-matched to `decision:executor-unary-rpc`'s unary Execute call but Go-idiomatic: the handler returns its outcome value directly instead of writing to a stream, and the handler-context struct gives it the few side-channel effects it legitimately needs without exposing runtime internals. Handlers stay simple and testable.

## Alternatives

Have handlers implement the gRPC executor server interface directly. Heavier — drags gRPC-server streaming machinery into handlers that don't need it.

An event-sink interface passed into Execute (a Send method emitting each event individually) — rejected: the executor protocol is unary, so there is nothing to stream; returning the Outcome directly is simpler and matches the other two transports.
