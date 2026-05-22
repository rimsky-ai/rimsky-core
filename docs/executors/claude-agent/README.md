# claude-agent

The `claude-agent` reference executor wraps the Claude CLI as a rimsky
executor, with first-class support for MCP catalogs, JSON Schema
validation on the agent's outputs, automatic rate-limit parking, and
session-resume on park.

## When to use it

- You want to run agent-driven analyses, transformations, or
  decisions inside a rimsky graph.
- You need MCP tool surfaces (project-built or third-party) wired
  into the agent's runtime.
- You want the platform to handle pause-and-resume on rate limits or
  external decisions without your template being aware.

For non-agent work, see `docs/executors/http-node/` (the bundled HTTP
executor) or implement a custom executor against
`protocols/proto/v1/executor.proto`.

## Configuration

claude-agent reads its startup config from
`CLAUDE_AGENT_CONFIG` (default `/etc/claude-agent/config.yaml`).
Required fields:

```yaml
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

Per-template attribute defaults reference catalog entries by name:

```yaml
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
        default: "Use the tools to complete the task."
```

The full expected-attributes schema is documented in
`docs/executors/claude-agent/expected-attributes.md`.

## Lifecycle features

- **`ObservabilityCapabilities.expected_attributes_schema`** — claude-agent
  declares its expected-attributes schema at handshake. Rimsky merges
  it with the template's L1 defaults and L2 per-node declaration to
  form the effective attribute schema, then validates the dispatch-time
  attribute bag against it at template registration and at dispatch
  (post-substitution).
- **Validate-on-`report_complete`** — the agent's structured output
  is validated against the dispatching node's `attributes_schema`.
  Failures up to `max_schema_corrections` (default 3) drive a
  corrective resume-prompt; the next call to `report_complete` is
  expected to fix it. Beyond the cap, claude-agent emits
  `Error { error_class: "schema_validation_failed" }`.
- **Auto rate-limit parking** — when the Claude CLI surfaces a 429,
  claude-agent emits `Park { reason: "rate_limit",
  resume_at: <reset_ts>, session_token: <session_id> }` and exits
  cleanly. The supervisor parks the node; the rimsky scheduler
  resumes it at `resume_at`, claude-agent restarts the CLI with
  `--resume <session_id>`, and the agent picks up where it left off.
- **Resume context** — if the dispatch carries `ResumeContext`,
  claude-agent extracts `session_token` for `--resume` and exposes
  `payload` and `resume_reason` to the prompt template via
  `{{rimsky.resume_payload}}` and `{{rimsky.resume_reason}}`.

## MCP transports

Four transports are supported:

- `http` — passes URL + headers to the Claude CLI's MCP config.
- `stdio` — spawns a subprocess; per-dispatch or persistent lifetime.
- `module` — alias for `http-loopback`; in-process module loading.
- `http-loopback` — imports a Node module, registers it with the
  MCP SDK, exposes it on a random local port to the Claude CLI.

The two-name surface for `module` and `http-loopback` exists for
documentation clarity (template authors can express intent — "this is
in-process tooling" — even when the wire path is identical).

## Build and test

```sh
cd executors/claude-agent
npm install
npm test
npm run build
```

Output goes to `dist/`.

## Operating

The executor is reachable via gRPC (default port from environment or
the Helm chart). Operators wire its endpoint into rimsky's
`executors:` block in `rimsky.yml`. It does not require a database;
state lives entirely in the dispatching supervisor's
`rimsky_node_runs` row plus the rimsky-side blob backend if
spilling is enabled.
