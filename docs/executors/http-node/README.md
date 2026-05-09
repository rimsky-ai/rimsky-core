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

## Userdata shape

```yaml
userdata:
  request:
    method: POST
    url: https://api.example.com/v1/items
    headers:
      Authorization: Bearer ${API_TOKEN}
    body_template: |
      {
        "name": "{{ params.name }}",
        "value": {{ nodes.upstream.value.score }}
      }
  response:
    success_codes: [200, 201]
    extract:
      id: $.data.id
      created_at: $.data.created_at
```

`body_template` runs through the standard substitution engine before
dispatch. `response.extract` uses JSONPath to populate
`attributes_delta` from the response.

## Behavior

- **`Complete`** is emitted on a status code in
  `response.success_codes` (default `[200]`). The extracted fields
  flow into `attributes_delta`.
- **`Errored`** is emitted on a non-success code; the response body
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
