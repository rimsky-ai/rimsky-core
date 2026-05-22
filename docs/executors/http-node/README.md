# http-node

`http-node` is the bundled Go reference executor for HTTP-call workloads.
A node configured with the `http-node` executor declares a target URL,
method, headers, and body; the executor performs the call and returns
the response in `attributes_delta`.

## When to use it

- Deterministic transformations that compose by HTTP request/response.
- Webhook-driven flows where a downstream system handles the work.
- Simple POST-an-event nodes for audit or notification side effects.

For agent-driven work, see `docs/executors/claude-agent/README.md`. For
custom logic that does not fit the HTTP-call shape, implement an
executor against `protocols/proto/v1/executor.proto`.

## Attribute shape

Under the post-2026-05-21 attribute surface, transport configuration
(`url`, `method`, `headers`, `body`, `expect_status`) lives in the
unified `attributes` schema alongside everything else the executor
reads. The executor splits the resolved attribute bag at dispatch:
the fixed set of "config" keys above drives the transport; every
other attribute key is serialised as the implicit JSON request body
(or overridden by an explicit `attributes.body`).

```yaml
attributes:
  schema:
    type: object
    properties:
      url:           { type: string, default: "https://api.example.com/v1/items" }
      method:        { type: string, default: "POST" }
      headers:
        type: object
        default:
          Authorization: "Bearer ${API_TOKEN}"
      expect_status: { type: array, default: [200, 201] }
      # Implicit-body keys: not in the config set, so they're
      # serialised as the JSON request body.
      name:          { type: string, source: "{{params.name}}" }
      value:         { type: number, source: "{{nodes.upstream.attribute.score}}" }
```

Source-bound attribute keys (`name`, `value` above) run through the
standard substitution engine before dispatch. Response handling is
configured on the same attribute bag — see `executors/http-node/server.go`
for the full set of `configAttributeKeys`.

## Behavior

- **`Complete`** is emitted on a status code in
  `response.success_codes` (default `[200]`). The extracted fields
  flow into `attributes_delta`.
- **`Error{error_class}`** is emitted on a non-success code; the response body
  is included in the payload for debugging.
- **`Errored { error_class: "transport" }`** is emitted on connection
  failure or timeout.
- **Heartbeat** is emitted at the rimsky-configured interval during
  long-running calls.

## Build and test

```sh
go build ./executors/http-node
go test ./executors/http-node/...
```

## Operating

http-node is stateless. Operators run it as a sidecar or as a
dedicated service; the operator config in `rimsky.yml` points at its
gRPC endpoint. The executor handles its own retry policy via the
caller's HTTP client; rimsky's retry policy is independent and lives
in the template's `on_executor_errored` handler.
