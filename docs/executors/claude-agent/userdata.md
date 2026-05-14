# claude-agent userdata schema

The `claude-agent` executor declares its userdata schema in
`Capabilities.userdata_schema`. Rimsky validates incoming template
userdata against this schema at template registration and at dispatch
(post-substitution).

This page documents every userdata field, its semantics, and
operator-side configuration.

## Top-level shape

```yaml
userdata:
  cli:
    model: ...
    system_prompt: ...
    user_prompt_template: ...
    allowedTools: [...]
    disallowedTools: [...]
    tools: [...]
    permissionMode: default | ask | deny
    max_schema_corrections: 3
    handle_rate_limits: true
    mcpServers: [...]
```

`additionalProperties: false` — keys outside the documented set reject
at registration.

## Fields

### `cli.model`

The model identifier passed to the Claude CLI's `--model` flag. When
absent, the CLI's default applies. Common values: `claude-opus-4-7`,
`claude-sonnet-4-5`, the model strings the CLI's `/model` command
exposes.

### `cli.system_prompt`

The system prompt the agent runs against. Substitution is applied
before dispatch (`{{...}}` directives resolve through the standard
attributes/params machinery). Userdata-side substitution is not
performed (`@blessed-invariant 11`); to interpolate values, use the
attributes substitution path.

### `cli.user_prompt_template`

The initial user prompt. Same substitution rules as `system_prompt`.

### `cli.allowedTools` / `cli.disallowedTools`

Forwarded to the Claude CLI's allow- and deny-lists. Names follow the
CLI's tool-naming convention. `tools` is a friendlier alias for
`allowedTools`.

### `cli.permissionMode`

One of `default`, `ask`, `deny`. Forwarded to the CLI.

### `cli.max_schema_corrections`

Default `3`. The number of consecutive `report_complete` validation
failures the agent is given before claude-agent emits
`Errored { error_class: "schema_validation_failed" }`. After each
failure, the executor invokes `--resume <session_id>` with a
corrective prompt and waits for the next `report_complete`.

### `cli.handle_rate_limits`

Default `true`. When true, claude-agent intercepts CLI rate-limit
errors (HTTP 429) and emits
`Park { reason: "rate_limit", resume_at: <reset_ts>, session_token: <session_id> }`.
Rimsky parks the node; on `resume_at` claude-agent resumes the CLI
with `--resume <session_id>`.

When false, rate-limit errors flow through as
`Error { error_class: "rate_limit" }`.

### `cli.mcpServers`

An array of MCP server entries. Each is either a `ref` to a catalog
entry or an inline definition.

#### Ref entries

```yaml
mcpServers:
  - ref: project-tracker
  - ref: workspace-files
    config:
      mode: read_only
```

The `ref` looks up an entry in the operator's startup-config catalog.
Optional `config` is shallow-merged into the catalog entry's `config`
field (only `module` and `http-loopback` transports have a `config`
field; refs against `http`/`stdio` ignore overrides).

Refs against unknown catalog entries reject the dispatch with a clear
error.

#### Inline entries

```yaml
mcpServers:
  - name: ad-hoc
    transport: http
    url: https://api.example.com
    headers:
      Authorization: bearer-token
```

Inline entries are accepted only when the operator's
`policy.allow_inline: true`. The strict default is `false`.

## Operator-side configuration

The MCP catalog and the `policy` block live in the executor's startup
config (`CLAUDE_AGENT_CONFIG`, default `/etc/claude-agent/config.yaml`):

```yaml
mcp_catalog:
  project-tracker:
    transport: http
    url: ${PROJECT_TRACKER_URL}
    headers:
      Authorization: ${PROJECT_TRACKER_TOKEN}
  workspace-files:
    transport: stdio
    command: project-fs-server
    args: ["--root", "/workspace"]
    lifetime: persistent
  alpha-tools:
    transport: module
    module: "@project-alpha/tools"
    lifetime: per-dispatch
    config:
      mode: default

policy:
  allow_inline: false
  allow_modules_from: ["@project-alpha/*"]
```

`${VAR}` and `${VAR:-default}` env-var indirection is resolved at load
time; values never carry env-var references downstream.

## MCP transports

### `http`

The simplest case: claude-agent passes the URL and headers through to
the Claude CLI's MCP server config (a per-dispatch `mcp.json` written
to a temp directory). The CLI dials the HTTP MCP server.

### `stdio`

Spawns a subprocess. `lifetime: per-dispatch` (default) creates and
reaps the subprocess for each dispatch. `lifetime: persistent` spawns
once per claude-agent process and exposes it on a known loopback port
to all dispatches via http (effectively converting stdio-persistent to
http internally).

### `module` and `http-loopback`

`module` is an alias for `http-loopback`. Both load a Node module at
dispatch time, register it with the MCP SDK, and expose it on a
loopback HTTP listener; the CLI sees the loopback URL as a regular
http-transport server.

`module` exists for documentation clarity (template authors can
express intent — "this is in-process tooling" — even though the wire
path is identical).

The `module` field must match an entry in `policy.allow_modules_from`
(glob patterns).

## Rate-limit auto-park

When `cli.handle_rate_limits` is true (default), claude-agent
intercepts CLI rate-limit reports and:

1. Captures the CLI's session_id (already tracked for resume-with-prompt).
2. Captures the rate-limit reset timestamp from the CLI's error output.
3. Emits `Park { reason: "rate_limit", resume_at: <reset_ts>, session_token: <session_id>, payload: {} }`.
4. Exits cleanly.

Rimsky parks the node. At `resume_at` the supervisor's parked-node
sweep wakes it; claude-agent receives `ResumeContext` with the same
`session_token`, launches the CLI with `--resume <session_id>`, and
the agent picks up.

## Schema validation on `report_complete`

The internal `report_complete` MCP tool validates `attributes_delta`
against the dispatching node's `attributes_schema` before committing
the terminal:

- Valid → proceed to terminal as today.
- Invalid → return an error to the agent through the MCP tool result
  and trigger resume-with-prompt with a corrective message:
  `"Your report_complete call failed schema validation: <error>. Please correct the output and call report_complete again."`

The corrective-retry count per dispatch is capped at
`cli.max_schema_corrections` (default 3). Beyond that, claude-agent
emits `Errored { error_class: "schema_validation_failed" }`.

## Resume context

When `ExecuteRequest.resume_context` is non-empty, claude-agent:

- Extracts `session_token` and launches the CLI with `--resume
  <session_token>` if set.
- Exposes `payload` to the prompt-template engine as
  `{{rimsky.resume_payload}}` (template authors opt in to use it).
- Exposes `resume_reason` as `{{rimsky.resume_reason}}`.

Both are template-author-visible context; never auto-injected into
prompts.

## Worked example

```yaml
# Template node
nodes:
  ingest:
    executor: claude-agent
    userdata:
      cli:
        model: claude-opus-4-7
        system_prompt: |
          You are an ingestion agent. Read the file and emit findings.
          Resume context (if present): {{rimsky.resume_payload}}
        max_schema_corrections: 5
        handle_rate_limits: true
        mcpServers:
          - ref: project-tracker
          - ref: workspace-files
    attributes:
      schema:
        properties:
          findings:
            type: array
            items: { type: string }
          confidence:
            type: number
        required: [findings]
```

```yaml
# Operator startup config
mcp_catalog:
  project-tracker:
    transport: http
    url: https://tracker.internal/mcp
    headers:
      Authorization: ${TRACKER_TOKEN}
  workspace-files:
    transport: stdio
    command: project-fs-server
    args: ["--root", "/data"]
    lifetime: persistent

policy:
  allow_inline: false
  allow_modules_from: []
```

A dispatch arrives; claude-agent resolves both refs, writes the
per-dispatch MCP config, launches the CLI. The agent calls
`tickets.list` (project-tracker) and `fs.read` (workspace-files), then
`report_complete` with structured findings. Rimsky receives the
terminal `Complete` and persists the attributes.
