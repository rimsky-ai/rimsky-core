# claude-agent expected attributes schema

The `claude-agent` executor declares its expected attribute schema in
`Capabilities.expected_attributes_schema`. Rimsky merges this with
template-level defaults (L1) and per-node declarations (L2) at template
registration to compute the effective attribute schema, then validates
the post-substitution attribute bag at dispatch and the post-write-back
bag at commit.

Under the 2026-05-21 userdata collapse there is no separate `userdata`
field on the wire or in the template — config and inputs live in a
single unified attribute bag. The executor reads `attributes.model`,
`attributes.system_prompt`, `attributes.user_prompt`, `attributes.cli.*`
directly.

## Top-level shape

```yaml
attributes:
  schema:
    properties:
      model:
        type: string
        default: claude-sonnet-4-5
      system_prompt:
        type: string
        default: "..."
      user_prompt:
        # Source-bound (rimsky resolves at dispatch via the substitution
        # grammar in graph/attribute/substitution.go) or static-default.
        type: string
        source: "Generate {{params.what}}.\n{{nodes.verify.attribute.warnings_block?}}"
      cli:
        type: object
        default:
          allowed_tools: [...]
          permission_mode: default | ask | deny
          max_schema_corrections: 3
          handle_rate_limits: true
          mcp_servers: [...]
```

The schema admits `additionalProperties: true` so authors may declare
extension attributes used purely for inter-node dataflow (e.g. a
`warnings_block` attribute used purely for cycle communication that the
executor doesn't read).

## Property shapes

Each schema property is one of three shapes, per `concept:attribute`:

- **source-bound** (`source:` directive): rimsky resolves at dispatch
  against the wait-set / claim / params / trigger / child via the
  substitution grammar. Per-directive strict-default with `?` opt-in
  to lenient (missing → null); mutually exclusive with `| <literal>`
  fallback. The substitution grammar admits literal text + multiple
  directives in a single source string.
- **static-default** (`default:` value, no source): resolved at
  registration from the effective schema. Replaces the role userdata
  played pre-collapse (template-author config baked into the template).
- **executor-written** (`readOnly: true` in
  `expected_attributes_schema`, no source, no default): populated at
  commit by the executor's write-back. The claude-agent executor today
  has no executor-written properties; the slot is reserved for future
  shapes (e.g. a `response_summary` field).

## Fields

### `model`

The model identifier passed to the Claude CLI's `--model` flag. The
schema declares `default: "claude-sonnet-4-5"`; templates may override
via L2 (`attributes.schema.properties.model.default: ...`), L1
(`defaults.attributes.by_executor.claude-agent.model: ...`), or
instance-time L3/L4/L5 overrides
(`attribute_overrides.{by_executor,by_node}.<...>.model: ...`, or a
matcher entry in `attribute_overrides.by_match` whose overlay sets
`model`).

### `system_prompt`

The system prompt the agent runs against. Resolved by rimsky at
dispatch (substitution applies to source-bound declarations); the
executor reads the resolved string verbatim and passes it to the CLI.
The system prompt does not receive the metadata footer (kept clean to
preserve prompt caching).

### `user_prompt`

The user prompt. Resolved by rimsky at dispatch; the executor reads
the resolved string and appends a fixed metadata footer carrying the
per-run callback token + resume metadata:

```
<user prompt content>

---
callback_token: <generated-uuid>
resume_payload: <bytes-as-utf8-or-empty>
resume_reason: <reason-or-empty>
---
```

The footer is always emitted; empty fields render as empty strings.
The agent (CLI subprocess) reads the `callback_token` from the footer
to call any rimsky-callback MCP tool. Templates that previously used
`{{rimsky.resume_payload}}` / `{{rimsky.resume_reason}}` placeholders
in the user_prompt template now read those values from the footer.

### `cli.allowed_tools` / `cli.disallowed_tools`

Forwarded to the Claude CLI's allow- and deny-lists. Names follow the
CLI's tool-naming convention.

### `cli.permission_mode`

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

### `cli.mcp_servers`

An array of MCP server entries. Each is either a `ref` to a catalog
entry or an inline definition.

#### Ref entries

```yaml
mcp_servers:
  - ref: project-tracker
  - ref: workspace-files
    config:
      mode: read_only
```

The `ref` looks up an entry in the operator's startup-config catalog.
Optional `config` is shallow-merged into the catalog entry's `config`
field (only `module` and `http-loopback` transports have a `config`
field; refs against `http`/`stdio` ignore overrides).

#### Inline entries

```yaml
mcp_servers:
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

policy:
  allow_inline: false
  allow_modules_from: ["@project-alpha/*"]
```

`${VAR}` and `${VAR:-default}` env-var indirection is resolved at load
time; values never carry env-var references downstream.

## Migrating from the pre-2026-05-21 userdata shape

The pre-collapse template node had `userdata:` for config and
`attributes:` for I/O. Post-collapse both fold into the unified
attribute schema. Mechanical mapping:

| Pre-2026-05-21 (retired)                    | Post-2026-05-21                          |
| ------------------------------------------- | ---------------------------------------- |
| `userdata.cli.model: "..."`                 | `attributes.schema.properties.model.default: "..."` (or L1 default) |
| `userdata.cli.system_prompt: "..."`         | `attributes.schema.properties.system_prompt.default: "..."` (or `.source: "..."`) |
| `userdata.cli.user_prompt_template: "..."`  | `attributes.schema.properties.user_prompt.source: "..."` |
| `{{userdata.X}}` in a prompt template       | `{{attributes.X}}` resolved at the rimsky layer; or use a `source:` directive at the attribute level |
| `{{rimsky.resume_payload}}` placeholder     | Reads from the metadata footer at the end of the user prompt |
| Instance `userdata_overrides`               | Instance `attribute_overrides` (same shape: `by_executor` + `by_node`, plus `by_match` matcher overlays — see `concept:attribute`'s "Matcher overlay (by_match)" section) |

## Worked example

```yaml
# Template node
nodes:
  - type: ingest
    executor: claude-agent
    attributes:
      schema:
        properties:
          model:
            type: string
            default: claude-opus-4-7
          system_prompt:
            type: string
            default: |
              You are an ingestion agent. Read the file and emit findings.
          user_prompt:
            type: string
            source: "Ingest {{params.target}}."
          cli:
            type: object
            default:
              max_schema_corrections: 5
              handle_rate_limits: true
              mcp_servers:
                - ref: project-tracker
                - ref: workspace-files
          findings:
            type: array
            items: { type: string }
            readOnly: true
          confidence:
            type: number
            readOnly: true
        required: [findings]
```

A dispatch arrives; rimsky resolves `user_prompt` (substituting
`{{params.target}}`), copies the static defaults for `model`,
`system_prompt`, and `cli` into the dispatch bag, applies L3/L4
overrides if any, validates, and sends the bag to the executor. The
executor appends the metadata footer to `user_prompt`, spawns the CLI,
and waits for `report_complete`. On terminal-success the executor's
write-back populates `findings` / `confidence`; rimsky validates the
result against the effective schema and persists.

## See also

- `concept:attribute` for the unified-attribute-surface model
- `concept:inertness` for the structural-inertness discipline on
  attribute values
- `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`
  for the spec that retired userdata
