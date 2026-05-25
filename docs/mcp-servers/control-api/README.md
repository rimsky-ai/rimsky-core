# rimsky-mcp-control-api

Bundled MCP shim that wraps the rimsky control-API as a tool catalog.
Useful for connecting agentic clients (Claude, Claude Code, custom MCP
clients) directly to a running rimsky deployment for template
registration, instance creation, parked-node inspection, and admin
invalidates — without writing a custom MCP server per integration.

## When to use it

- You want an LLM agent to drive rimsky operationally (register a
  template, create an instance, inspect held frames, resume a parked
  node).
- You want to expose rimsky's admin surface to a client that speaks
  MCP without exposing the raw HTTP API to the same client.
- You want a single shim per deployment rather than re-implementing
  the wrapper layer in each consumer.

## Configuration

The shim reads four environment variables:

| Variable             | Default                  | Description                                                             |
|----------------------|--------------------------|-------------------------------------------------------------------------|
| `CONTROL_API_URL`    | `http://127.0.0.1:8080`  | Absolute base URL of the rimsky control-API.                            |
| `CONTROL_API_TOKEN`  | (empty)                  | When set, forwarded as `Authorization: Bearer <token>` to control-API.  |
| `BIND_ADDR`          | `0.0.0.0`                | Bind address for the shim's HTTP listener.                              |
| `PORT`               | `8081`                   | Listen port for the shim's HTTP listener.                               |

Run:

```sh
CONTROL_API_URL=http://rimsky-control-api:8080 \
CONTROL_API_TOKEN="$RIMSKY_TOKEN" \
go run github.com/fallguyconsulting/rimsky/mcp-servers/control-api/cmd/rimsky-mcp-control-api
```

## Wire protocol

JSON-RPC 2.0 over `POST /mcp`. The shim implements three methods:

- `initialize` — returns `protocolVersion`, `serverInfo`, `capabilities`.
- `tools/list` — returns the registered tool catalog.
- `tools/call` — dispatches by tool name; arguments are JSON-Schema
  validated server-side.

## Tool catalog

Each tool is a thin pass-through over the rimsky control-API.

### Templates

- `template_list` — list registered templates.
- `template_get { hash }` — fetch template by content hash.
- `template_register { spec, tag?, source? }` — register a new template.
- `template_deploy { hash }` — mark a template deployed.
- `template_undeploy { hash }` — mark a template undeployed.
- `template_deregister { hash }` — delete a template.

### Tags

- `tag_list` — list tags.
- `tag_set { name, hash }` — point a tag at a template hash.
- `tag_delete { name }` — delete a tag.

### Instances

- `instance_list` — list instances.
- `instance_get { id }` — fetch an instance.
- `instance_create { template, instance_key?, params?, attribute_overrides? }`
  — create a new instance. `attribute_overrides` mirrors the rimsky
  control-API's per-instance overrides surface
  (`{by_executor: {...}, by_node: {...}, by_match: [...]}`). `by_match`
  is an ordered list of `{matcher, overlay}` entries whose matcher is a
  content-keyed predicate (`node_type`, `executor`, `graph`,
  `child_key`, `attrs.<path>`) — see `concept:attribute`'s "Matcher
  overlay (by_match)" section. `instance_get` echoes
  `attribute_overrides_match_counts` (per-entry counter) alongside the
  overrides blob.
- `instance_terminate { id }` — terminate an instance.

### Nodes

- `node_get { instance, node_id }` — fetch a node.
- `node_invalidate { instance, node_id }` — wake a parked node or
  fresh-invalidate a fresh node. Maps to `POST
  /admin/instances/{instance}/nodes/{node_id}/invalidate`. Returns
  409 when the node is in a state that does not accept invalidates
  (running | failed).
- `force_fire_scheduled { node_id }` — advance a scheduled node's
  `next_fire_at` so the next scheduler tick picks it up.

### Diagnostics

- `held_frames_list` — frames with at least one parked node.
- `parked_nodes_list { reason? }` — currently parked nodes; optional
  reason filter.

## Deployment shape

The shim is a stateless sidecar; co-locate it with the rimsky
control-API. Two reasonable deployments:

- **Sidecar:** the shim runs in the same pod / container group as
  rimsky-control-api and binds to `127.0.0.1`. Agentic clients in the
  same network connect to the shim; rimsky-control-api stays
  network-private.
- **Separate container:** the shim runs in its own container/pod and
  reaches rimsky-control-api over the cluster network. Use a
  network policy to keep direct access to rimsky-control-api closed.

## Security

- The shim does not enforce its own auth. Operators should isolate
  the shim's port via network policy / firewall.
- The shim forwards `CONTROL_API_TOKEN` as a bearer token to the
  underlying control-API. The token is not logged.
- All inputs are JSON-Schema validated against the rimsky control-API
  schemas before dispatch; the shim rejects malformed bodies with
  JSON-RPC `-32602` (invalid params).
