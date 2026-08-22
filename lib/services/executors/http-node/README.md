# http-node executor

Reference rimsky node executor for the `http.request@1` node type. Runs an
outbound HTTP request based on its `attributes` and returns a single
`Outcome` (`Success`, `Error`, or `Park`).

## Transports

The executor exposes two transports simultaneously, both backed by the same
`Server.Execute` implementation:

- **gRPC** on `RIMSKY_EXECUTOR_PORT_GRPC` (default `9091`) — primary
  transport used by the rimsky supervisor.
- **HTTP+JSON bridge** on `RIMSKY_EXECUTOR_PORT_HTTP` (default
  `9092`, i.e. grpc port + 1). POST a protojson-encoded `ExecuteRequest` to
  `/v1/Execute` and receive a single protojson-encoded `Outcome` in the
  response body.

## Attribute schema

The executor advertises its expected-attributes schema over the
observability protocol. The schema declares every attribute the executor
reads and admits no other, so template registration rejects a node whose
attribute block names anything else.

```
{
  "url": "https://api.example.com/v1/things",
  "method": "POST",                              // optional; default GET
  "headers": { "Authorization": "Bearer ..." },  // optional
  "body": { "fixed": "payload" },                // optional; the upstream request body
  "expect_status": [200, 202],                   // optional; default 2xx
  "error_class_field": "code",                   // optional; per-request override of RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD
  "stub_probe": true,                            // optional; used only in stub mode
  "stub": true                                   // read-only; the stub-mode success marker
}
```

`url` is required in practice: a dispatch without it settles as
`http/attribute_invalid`.

## Request body

`attributes.body` is the upstream request body. A string is sent as
written; any other value is JSON-serialised and sent with
`Content-Type: application/json`. With no `body`, no body is sent.

## Response → attributes_delta

The upstream response body becomes the `Success.attributes_delta`. A
`Content-Type` containing `json` must decode to a JSON object — arrays,
scalars, and malformed JSON are rejected as `http/response_unparseable`.
Non-JSON content types are wrapped as
`{ "body_base64": "...", "content_type": "..." }` so the bytes are still
visible to downstream nodes. A response body over the size cap
(`RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES`) is rejected outright as
`http/response_truncated` rather than silently delivered as a truncated,
possibly corrupt, `Success`.

## Error classes

Declared in `errorclasses.Declared()`, advertised identically over both the
gRPC `Capabilities` RPC and the HTTP `/observability/v1/capabilities`
endpoint:

| class | when |
| --- | --- |
| `http/attribute_invalid` | `url` missing, body not JSON-serialisable, request-construction failure, non-object `stub_response` |
| `http/network_error` | network error other than a timeout |
| `http/timeout` | request deadline exceeded |
| `http/request_invalid/<class>` | 4xx status; `<class>` is the value at `error_class_field` in a JSON error body, or `_unspecified` |
| `http/server_error/<code>` | 5xx status; `<code>` is the numeric HTTP status |
| `http/expectation_mismatch` | status not in `expect_status` and not classifiable as request/server error |
| `http/response_unparseable` | JSON Content-Type with invalid or non-object body |
| `http/response_truncated` | response body exceeds the size cap |
| `http/internal_error` | unexpected internal failure (HTTP bridge only) |

A `429` status not in `expect_status` parks the dispatch (`Outcome_Park`,
tagged `rate_limited`) with `resume_at` derived from the `Retry-After`
header, instead of erroring.

## Stub mode

Set `RIMSKY_EXECUTOR_STUB_MODE=1` to short-circuit the network path. Execute
always returns `{"stub": true}` as the `Success.attributes_delta`, or
`attributes.stub_response` if supplied (must be a JSON object). Required for
offline scenario tests.

## Outbound egress guard

Outbound requests are dialed through the shared SSRF guard
(`lib/services/internal/egress`): private/loopback/link-local/metadata
destinations are blocked by default. Opt a CIDR back in via
`RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST` (comma-separated CIDRs/IPs).

## Env vars

| var | default | purpose |
| --- | --- | --- |
| `RIMSKY_EXECUTOR_HOST` | `0.0.0.0` | bind host for both transports |
| `RIMSKY_EXECUTOR_PORT_GRPC` | `9091` | gRPC port (also honors `RIMSKY_AGENT_PORT` for late-bound spawns) |
| `RIMSKY_EXECUTOR_PORT_HTTP` | `grpc+1` | HTTP+JSON bridge port |
| `RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES` | `10485760` | response-body size cap |
| `RIMSKY_EXECUTOR_HTTP_NODE_HTTP_BRIDGE_URL` | `` | advertised HTTP-bridge URL surfaced in observability capabilities |
| `RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD` | `error_class` | JSON key read from 4xx error bodies to build `http/request_invalid/<class>` |
| `RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST` | (unset) | comma-separated CIDRs/IPs to opt back into the default-blocked egress guard |
| `RIMSKY_EXECUTOR_STUB_MODE` | `0` | `1` to enable stub mode |

## Build and test

```
go build ./lib/services/executors/http-node/cmd
go test ./lib/services/executors/http-node/... -count=1
```

The executor imports only `lib/protocols` and `lib/services/internal/*` —
never `lib/graph`, `lib/runtime`, or `lib/control` — per the
`consumption-side-isolation` depguard rule.
