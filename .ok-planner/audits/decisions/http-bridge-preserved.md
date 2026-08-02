---
audit: http-bridge-preserved
artifact: decision:http-bridge-preserved
determination: supported
commit: b767a27d
audited: 2026-08-02T09:44:46Z
---

# claude-agent keeps an HTTP-JSON bridge alongside its gRPC surface

Supported. `lib/services/executors/claude-agent/httpbridge.go`'s `StartHTTPBridge` mounts a health endpoint (`/healthz`), an execute endpoint (`/execute`) that decodes a JSON dispatch body and replies `202` with `{"async_ack_id": ...}`, and the observability capability/trace routes (`/observability/v1/capabilities`, `observability.MountTraceBridge`), all on a port configurable via `RIMSKY_EXECUTOR_PORT_HTTP` (`opts.go`). `serve.go`'s `Serve` — the standalone binary's entrypoint — starts both this HTTP bridge and the gRPC server; the in-process path (`lib/services/bundled/bundled.go`) instead constructs `claudeagent.NewExecutorServer` directly and never calls `Serve`, so in-process dispatch bypasses both transports as claimed. `lib/services/executors/claude-agent/httpbridge_test.go` exercises healthz, execute-plus-callback, oversized-body rejection, and the two observability routes end to end.
