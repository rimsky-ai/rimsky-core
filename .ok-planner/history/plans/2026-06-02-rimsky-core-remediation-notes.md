# Rimsky Core Remediation — Implementer Notes

Scratch notes captured during plan execution. Workflow scratch, not durable docs.

---

## Pass 4 / Task 8 (REPRO): MCP `/mcp` connect-and-control handshake surface (#7)

### Goal

Pin exactly what the default Claude Code `type: http` MCP client needs from
`/mcp` to *connect and control* (initialize, list tools, call tools). Live
server-push / subscriptions are explicitly V2 and out of scope.

### How the default `type: http` client talks to a server (MCP Streamable HTTP)

The default Claude Code `type: http` client speaks the **MCP Streamable HTTP**
transport (the `2025-03-26`+ transport; protocol version we advertise is
`2025-06-18`). The connect-and-control handshake the client performs is:

1. `POST /mcp` with `{"method":"initialize",...}`, `Accept: application/json,
   text/event-stream`. The server replies with the `initialize` result. If the
   server is session-aware it returns an `Mcp-Session-Id` response header; the
   client then echoes that header on every subsequent request.
2. `POST /mcp` with `{"method":"notifications/initialized"}` — a JSON-RPC
   **notification** (no `id`). Per JSON-RPC 2.0 a notification MUST NOT receive a
   response. The correct HTTP reply is `202 Accepted` with an empty body.
3. `GET /mcp` (with `Accept: text/event-stream` and the session header) — the
   client opens a server-to-client SSE stream to receive any server-initiated
   messages. The server must answer `200` with `Content-Type:
   text/event-stream`. A server with nothing to push may hold the stream open
   and idle (optionally emitting SSE keep-alive comments).
4. `POST /mcp` `tools/list`, then `tools/call`, each carrying the session header.

### Where the current server breaks the handshake

Current code: `Server.ServeHTTP` is JSON-RPC-over-POST only, always replies
`application/json`, issues no session id, and routes `notifications/initialized`
into the `default` switch arm which calls `writeRPCError(... CodeMethodNotFound
...)`. The route layer registers only `POST /mcp`, so chi answers `GET /mcp`
with `405 Method Not Allowed`.

Three concrete failures, in handshake order:

1. **No session id on `initialize`.** The `initialize` response carries no
   `Mcp-Session-Id` header. The client can still proceed (session id is optional
   per spec), but a session-aware server is the expected shape and the test
   asserts one is issued. → must set an `Mcp-Session-Id` response header on
   `initialize`.

2. **`notifications/initialized` gets an erroneous JSON-RPC error reply.** It
   falls through to the `default` arm → `method not found` error body. This is a
   JSON-RPC violation: a notification (no `id`) must get no response body. The
   default client treats the spurious error as a handshake failure. → must
   consume any `notifications/*` (id-less request) and reply `202 Accepted`,
   empty body, no JSON-RPC envelope.

3. **`GET /mcp` returns 405.** Only `POST /mcp` is registered; chi's
   method-not-allowed handler answers `GET` with `405`. The client's SSE stream
   probe fails. → must register a `GET /mcp` handler that returns `200` /
   `text/event-stream` (a valid, possibly idle, keep-alive stream). No domain
   push is required — V1 has no server-initiated notifications; the stream may
   stay open and idle.

### Minimal transport surface required to connect

To make the default `type: http` client connect and control, the server needs:

- **`initialize` issues a session id** — return an `Mcp-Session-Id` response
  header (a fresh opaque id per initialize). Accept the same header on
  subsequent requests (echo / validate; V1 need not reject a missing/unknown id
  — it is stateless beyond connect, since there is no server-push state to bind
  to a session).
- **`notifications/*` (any id-less JSON-RPC request) is consumed with no
  response** — reply `202 Accepted`, empty body. Covers
  `notifications/initialized` and any other notification the client emits.
- **`GET /mcp` opens a valid `text/event-stream`** — `200`, `Content-Type:
  text/event-stream`, may stay idle/keep-alive. Replaces the `405`.
- **Existing POST tool/resource behavior unchanged** — `initialize` /
  `tools/list` / `tools/call` / `resources/list` / `resources/read` keep their
  current results.

### Out of scope (V2, deliberately cut)

Server-initiated notifications / `resources/subscribe` /
`notifications/resources/updated` / live event streaming. The `GET /mcp` stream
exists only so the client's probe succeeds; it pushes nothing in V1.

### Test plan (feeds Tasks 9–10)

`TestMCPStreamableHTTPHandshake` in `lib/control/controlapi/mcp/server_test.go`
(no Docker — drives `Server.ServeHTTP` directly + the registered GET handler):
`initialize` (assert `Mcp-Session-Id` header present) → `notifications/initialized`
(assert `202`, empty body, NO JSON-RPC error) → `tools/list` → `tools/call`
(both succeed) → `GET /mcp` (assert `200` + `text/event-stream`, not `405`).

Live reproduction against a running control-api + a Claude Code `.mcp.json`
`type: http` entry could not be exercised inside this automated implementer
environment (no live control-api stack / interactive client available here); the
transport surface above is derived from the MCP Streamable HTTP transport spec
and the current code's concrete divergences from it, which the handshake test
encodes and asserts. (Per the plan's "Manual checks after completion," the user
may run the live `type: http` connection check; the automated handshake test is
the standing gate.)
