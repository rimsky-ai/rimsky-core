---
concept: domain-stores
definition: |
  Consumer-built MCP servers that hold project-specific resources (records, jobs, queue items) and expose them as claim producers. The colloquial "stores" terminology at the bundled-services layer.
proto_symbol: ClaimProducer in protocols/proto/v1/claim_producer.proto
config_field: rimsky.yml:claim_producers
api_surface: (none)
related: [claim-producer, claim, scope]
deprecated_terms: []
---

# Domain stores

A **domain store** is a project-built MCP server that holds
project-specific state — prompt context, learnings, examples,
corrections, glossaries, evaluation rubrics, partial pipeline state —
and exposes it as a tool catalog that an agent executor can consume
during a dispatch. Rimsky does not ship a domain store; the platform
provides the wiring that makes them composable.

This page covers the pattern and the contract a domain store satisfies
to participate in the rimsky control plane.

## What a domain store is

Mechanically: an HTTP or stdio MCP server registered in the executor's
catalog (claude-agent uses an `mcp_catalog` block in its startup
config — see `docs/executors/claude-agent/expected-attributes.md`). Templates
reference catalog entries by `ref` in their attribute schema's `cli.mcpServers`
default. Dispatch wires the entry into the agent's tool space.

Conceptually: rimsky stays domain-agnostic; the domain store is where a
consuming project lands its vocabulary, its data, and its operations.
The store presents the project as a tool surface to agents, while the
graph (nodes, attributes, claims) handles flow control.

## Why this split

- **Re-use across templates.** A single project-tracker MCP can serve
  a dozen graphs without each template re-defining the integration.
- **Operator-managed catalogs.** Catalog entries live in the operator's
  startup config; templates only carry refs. Adding a new domain store
  is an operator change, not a template change.
- **Auditable tool surfaces.** `policy.allow_inline: false` blocks
  templates from injecting unconfigured MCP servers at dispatch time.
  The operator's allow-list is the single source of truth for which
  MCP surfaces dispatches may reach.

## The minimal contract

A domain store needs:

1. An MCP-protocol HTTP or stdio endpoint.
2. A `tools/list` response advertising its tool catalog.
3. Per-tool input schemas for argument validation.
4. Idempotency on the tools that can be retried by the agent
   (rimsky's retry policy may dispatch the same node multiple times).

What rimsky does **not** require:

- Any persistence model (use Postgres, SQLite, files, an in-memory
  cache, or a remote service).
- Any specific authentication scheme (the operator wires this through
  the catalog config — env vars, headers, mTLS).
- A specific language or runtime.

## Worked example: a project-tracker domain store

```yaml
# operator config
mcp_catalog:
  project-tracker:
    transport: http
    url: ${PROJECT_TRACKER_URL}
    headers:
      Authorization: ${PROJECT_TRACKER_TOKEN}
policy:
  allow_inline: false
  allow_modules_from: ["@project-alpha/*"]
```

```yaml
# template node
nodes:
  ingest:
    executor: claude-agent
    attributes:
      schema:
        type: object
        properties:
          cli:
            type: object
            default:
              mcpServers:
                - ref: project-tracker
          system_prompt:
            type: string
            default: "Use tools to fetch tickets and update status."
```

The project-tracker server exposes `tickets.list`, `tickets.get`,
`tickets.update_status` and similar. The agent invokes them through
MCP. The dispatch's `attributes_delta` carries the agent's structured
findings back into rimsky.

## Substituting domain-store outputs into the graph

When a domain-store call's result is needed by a downstream node,
expose it through the standard substitution path. Two main idioms:

- **Through the agent's `report_complete`.** The agent stores its
  findings in `attributes_delta`; downstream nodes read them via
  `source: nodes.<this>.value.<path>`.
- **Through named events.** If the executor emits non-terminal
  domain-shaped signals (e.g. `ticket.updated`, `learning.captured`),
  downstream nodes read them via
  `source: nodes.<this>.event.<name>.<path>`.

Pick whichever shape the data fits — `value` for terminal outputs,
`event` for mid-flight signals — and the substitution layer treats
them identically once persisted.

## Operational notes

- **Per-dispatch vs. persistent lifetimes.** Stdio MCP servers can run
  per-dispatch (spawned and reaped each run) or persistent (one
  process per claude-agent process). Persistent is the cheaper choice
  for stateless tooling.
- **Module / http-loopback transports.** For tools tightly coupled to
  the executor's runtime (e.g. project-shaped helpers in TypeScript),
  prefer the `module` transport. It runs in-process and the wire path
  collapses to a loopback HTTP listener.
- **Audit logging.** Domain stores are the right place for audit
  trails of agent-driven mutation. Rimsky's events log captures node
  state transitions; the domain store captures domain-level state
  changes.
