---
decision: inproc-handler-interface
---

# What in-process utility executors implement

## Choice

In-process utility executors implement a small Go interface in the runtime executor package: one Execute method taking a context, the executor's execute-request DTO, and a handler-context struct carrying the cascade-message sender — the handler's one side channel. The handler returns the terminal Outcome DTO directly, or an error. Scratch is not a side channel: it rides the request and the outcome messages like any other payload field (see `decision:scratch-protocol`). Translating a handler error into an error terminal belongs to the shared dispatch layer, which does it for every transport alike. Generated protobuf types are passed as DTOs at the function-call boundary — no wire encoding.

## Rationale

Shape-matched to `decision:executor-unary-rpc`'s unary Execute call but Go-idiomatic: the handler returns its outcome value directly instead of writing to a stream, and the handler-context struct gives it the one side-channel effect it legitimately needs without exposing runtime internals. Handlers stay simple and testable. Putting error translation on the shared dispatch layer rather than the in-process client keeps one behavior across the three transports instead of three copies.

## Alternatives

Have handlers implement the gRPC executor server interface directly. Heavier — drags gRPC-server streaming machinery into handlers that don't need it.

An event-sink interface passed into Execute (a Send method emitting each event individually) — rejected: the executor protocol is unary, so there is nothing to stream; returning the Outcome directly is simpler and matches the other two transports.

A scratch accessor on the handler-context struct — rejected: scratch already travels on the request and outcome messages, so an accessor would be a second channel for one thing.
