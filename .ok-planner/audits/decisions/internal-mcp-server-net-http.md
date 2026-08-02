---
audit: internal-mcp-server-net-http
artifact: decision:internal-mcp-server-net-http
determination: supported
commit: b767a27d
audited: 2026-08-02T09:44:46Z
---

# The internal per-dispatch MCP callback server is a bare net/http JSON-RPC server

Supported. `lib/services/executors/claude-agent/internalmcpserver.go`'s `startMcpHTTPServer` builds the callback endpoint entirely on `net/http` (`http.Server`, `http.NewServeMux`) and hand-parses a small JSON-RPC subset (`initialize`, `ping`, `tools/list`, `tools/call`, session delete) — no third-party MCP library is imported anywhere in the package. `Close()` calls `http.Server.Shutdown`, whose documented semantics wait for the in-flight `tools/call` handler (which writes the terminal-report JSON-RPC response synchronously before the async teardown goroutine that triggers `Close` even runs) to finish before the listener stops, so the child's response is guaranteed delivered first. `StartInternalMcpServer` (the per-dispatch callback server) and `resolveHostServers`'s module-transport loopback endpoints (`moduleloopback.go`) both call the same `startMcpHTTPServer` core. `internalmcpserver_test.go` covers 15 scenarios against this server including `report_complete`/`report_blocked`/`report_error`/`report_park` and session lifecycle.
