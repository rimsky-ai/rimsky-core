---
audit: inproc-handler-interface
artifact: decision:inproc-handler-interface
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:52Z
---

# In-process handlers implement a single Execute method over DTOs, not the gRPC server interface

Supported. `lib/runtime/executor/inproc_handler.go` declares `InProcessHandler` as exactly one method, `Execute(ctx, *genv1.ExecuteRequest, HandlerContext) (*genv1.Outcome, error)`, using the generated protobuf request/outcome types directly as Go DTOs (no wire encoding), plus a `HandlerContext` struct exposing only `SendCascadeMessage` as a side-channel effect. All three registered builtin handlers (`loop_counter`, `attribute_passthrough`, `send_message`) and the bundled-service adapter (`bundledwire.inprocExecutorAdapter`) implement this exact shape, and `InProcessClient.Execute` translates a handler error into an error terminal rather than requiring the handler to construct one itself.
