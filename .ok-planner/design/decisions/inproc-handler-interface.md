---
decision: inproc-handler-interface
status: as-is
aliases: []
---

# What in-process utility executors implement

## Choice

In-process utility executors implement a small Go interface in the runtime executor package: one Execute method that takes a context, the executor's execute-request DTO, and an event-sink interface; the handler returns nil on success or an error the in-process client translates into an error terminal. The event-sink interface has one Send method that accepts an execute-event DTO. Generated protobuf types are passed as DTOs at the function-call boundary — no wire encoding.

## Rationale

Shape-matched to the gRPC server-streaming Execute method but Go-idiomatic. Handlers stay simple and testable.

## Alternatives

Have handlers implement the gRPC executor server interface directly. Heavier — drags gRPC-server streaming machinery into handlers that don't need it.
