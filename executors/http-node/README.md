# http-node executor

Reference rimsky node executor for the `http.request@1` node type. Runs an
outbound HTTP request based on `userdata` and emits a single terminal
StreamClose `ExecuteEvent` carrying a `Success` outcome on 2xx JSON or
binary, otherwise an `Error{error_class}` outcome.

## Transports

The executor exposes two transports simultaneously:

- **gRPC** on `RIMSKY_EXECUTOR_HTTP_NODE_PORT` (default `9091`) — primary
  transport used by the rimsky supervisor.
- **HTTP+JSON bridge** on `RIMSKY_EXECUTOR_HTTP_NODE_HTTP_PORT` (default
  `9092`, i.e. grpc port + 1). POST a protojson-encoded `ExecuteRequest` to
  `/v1/Execute` and receive a newline-delimited stream of protojson
  `ExecuteEvent`s.

Both transports share the same `executeCore` implementation via a
transport-independent `sendFunc`.

## Userdata schema

```
{
  "url": "https://api.example.com/v1/things",
  "method": "POST",                              // optional; default GET
  "headers": { "Authorization": "Bearer ..." },  // optional
  "body": { "fixed": "payload" },                // optional override; if set, takes precedence over `attributes`
  "expect_status": [200, 202],                   // optional; default 2xx
  "stub_response": { "id": "abc" }               // optional; used only in stub mode (must be a JSON object)
}
```

`url` is required. All other fields are optional. Userdata is opaque to
rimsky — only this executor inspects it.

## Request body

By default the per-run `attributes` map (populated by rimsky at dispatch and
delivered in `ExecuteRequest.attributes`) is JSON-serialised and sent as the
upstream request body with `Content-Type: application/json`. `userdata.body`
is an explicit override useful for fixture tests; when set it wins and
`attributes` is not appended. With neither set, no body is sent.

## Response → attributes_delta

The upstream response body is the executor's terminal-StreamClose
`Success.attributes_delta` writeback (spec §12.2). It must be a JSON object
— JSON arrays/scalars are rejected as `http_response_parse_failed`. Non-JSON
Content-Types are wrapped as `{ "body_base64": "...", "content_type": "..." }`
so the bytes are still visible to downstream nodes.

## Error classes

| class | when |
| --- | --- |
| `invalid_userdata` | `url` missing, body not JSON-serialisable, request-construction failure, non-object `stub_response` |
| `http_request_failed` | network error or body read failure |
| `http_unexpected_status` | response status not in `expect_status` |
| `http_response_parse_failed` | JSON Content-Type with invalid body, or non-object JSON response |

## Stub mode

Set `RIMSKY_EXECUTOR_STUB_MODE=1` to short-circuit the network path. Execute
always returns `{"stub": true}` as the terminal-StreamClose
`Success.attributes_delta`, or `userdata.stub_response` if supplied (must be
a JSON object). Required for offline scenario tests (spec §14.4, Plan B
Phase 3).

## Env vars

| var | default | purpose |
| --- | --- | --- |
| `RIMSKY_EXECUTOR_HTTP_NODE_HOST` | `0.0.0.0` | bind host for both transports |
| `RIMSKY_EXECUTOR_HTTP_NODE_PORT` | `9091` | gRPC port |
| `RIMSKY_EXECUTOR_HTTP_NODE_HTTP_PORT` | `grpc+1` | HTTP+JSON bridge port |
| `RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS` | `60000` | per-request upstream timeout |
| `RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES` | `10485760` | response-body size cap |
| `RIMSKY_EXECUTOR_STUB_MODE` | `0` | `1` to enable stub mode |

## Build and test

```
go build ./executors/http-node/
go test ./executors/http-node/... -count=1
```

The executor imports nothing from `core/*` — only `proto/v1/gen` — per the
three-collections separation rule.
