// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

/**
 * JSON Schema for claude-agent's accepted attribute shape
 * (`expected-attributes-schema.ts`).
 *
 * Drives two paths:
 *   1. Returned in `Capabilities.expected_attributes_schema` so rimsky
 *      can merge into the per-node effective attribute schema at
 *      template registration and validate the resolved attribute bag
 *      at dispatch.
 *   2. Used internally for input validation when claude-agent receives
 *      a dispatch (defence in depth — rimsky already validated, but
 *      the executor re-checks before launching the CLI).
 *
 * Per the 2026-05-21 userdata-collapse, the executor advertises a
 * single unified attribute schema (no separate `userdata_schema`).
 * Inputs (`system_prompt`, `user_prompt`, `model`, `cli.*`) carry no
 * `readOnly` marker; outputs would carry `readOnly: true` (none in
 * claude-agent today). The schema admits `additionalProperties: true`
 * so authors may declare extension attributes used purely for inter-
 * node dataflow (e.g. a `warnings_block` attribute used in a producer-
 * owned-recovery cycle that this executor does not read).
 *
 * Schema property names match parseCliConfig in server.ts /
 * http-bridge.ts: snake_case. Mismatched names (camelCase here +
 * snake_case in the parser) would cause templates that use the
 * real key names to fail schema validation, while templates using
 * camelCase would pass validation but be silently ignored at run
 * time. The parser is the source of truth; schema follows it.
 */
export const expectedAttributesSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  type: "object",
  properties: {
    // @deliberate: cwd_from_store / cwd are top-level attribute fields read by
    // server.ts and http-bridge.ts to resolve the agent's working
    // directory. Both default to empty string so a template that
    // doesn't override them clears rimsky's "property has no
    // `source:` / `default:` / `readOnly`" composition check
    // (template_validator.go::checkAttributesSchema); an empty
    // value is treated by the dispatch entrypoint as "no override"
    // (the cwd resolution logic falls through to the default).
    cwd_from_store: { type: "string", default: "" },
    cwd: { type: "string", default: "" },
    // @deliberate: model / system_prompt / user_prompt are read at the top level
    // by the dispatch entrypoint in agent-run.ts. They are NOT under
    // `cli.*` even though they affect the spawned CLI. Keeping them
    // at the schema's top level matches the dispatch entrypoint.
    model: { type: "string", default: "claude-sonnet-4-5" },
    system_prompt: { type: "string" },
    // @deliberate: user_prompt is the fully rimsky-resolved user prompt (post-
    // collapse the executor reads the resolved value verbatim and
    // appends a fixed metadata footer; the old `user_prompt_template`
    // two-stage substitution is gone).
    user_prompt: { type: "string" },
    // @concept: attribute
    // @story: claude-agent-session-resume
    //
    // session_token is the claude-agent-owned CLI session identity
    // that rides the rimsky attribute carry-forward mechanism. The
    // executor writes the current dispatch's runId here on every
    // terminal Success via the attributes_set callback; rimsky's
    // self-state carry-forward makes the value visible on the next
    // dispatch of the same node within the same RunScope. When
    // non-empty on a fresh ExecuteRequest, the executor launches the
    // CLI with `--resume <session_token>` so the prior conversation
    // continues. Sub-graph and fan-out RunScopes start fresh —
    // carry-forward is RunScope-bounded — so a sub-graph invocation
    // of an agent template
    // begins a fresh CLI conversation.
    session_token: {
      type: "string",
      readOnly: true,
      default: "",
    },
    cli: {
      type: "object",
      // @deliberate: the cli.* fields below are exactly those parseCliConfig
      // reads (server.ts / http-bridge.ts). Adding a field here
      // without a matching reader would silently no-op at runtime;
      // adding a reader without a schema entry would reject
      // legitimate templates at dispatch validation. Keep the
      // schema and the parser in lock-step.
      properties: {
        bare: { type: "boolean" },
        allowed_tools: { type: "array", items: { type: "string" } },
        disallowed_tools: { type: "array", items: { type: "string" } },
        add_dirs: { type: "array", items: { type: "string" } },
        max_budget_usd: { type: "string" },
        permission_mode: {
          // @deliberate: real Claude CLI `--permission-mode` values. `bypassPermissions`
          // is the executor's own default (cli-runner.ts). The prior
          // ["default","ask","deny"] set was wrong: it rejected the
          // executor's default and omitted every other real mode.
          type: "string",
          enum: ["default", "acceptEdits", "bypassPermissions", "plan"],
        },
        max_schema_corrections: {
          type: "integer",
          minimum: 0,
          default: 3,
        },
        handle_rate_limits: {
          type: "boolean",
          default: true,
        },
        // @deliberate: sign-off gate (parsed by parseCliConfig as mcpServers /
        // requiredSignoffs / maxSignoffAttempts). Host-wired validator
        // MCP servers and the required (public_key, path) signature
        // pairs. Kept in lock-step with the parser per the comment above.
        //
        // Two entry shapes (S-executors-mcp-catalog-transports), mirrored
        // by parseMcpServers in server.ts / http-bridge.ts:
        //   - inline { name, url, headers?, allowed_tools? } — declared
        //     on the node, permitted only when the executor's
        //     allow_inline policy is true.
        //   - catalog reference { ref } — resolved at dispatch against
        //     the startup catalog the executor loaded from
        //     RIMSKY_EXECUTOR_MCP_CATALOG.
        //
        // The schema mirrors the parser's `if ("ref" in e) { ... }` /
        // inline-fallthrough split: anyOf admits either shape, neither
        // requires the other's fields. Rejecting one of the two valid
        // shapes here would force every template wiring a catalog ref
        // to fail composition validation even though the dispatch path
        // would have accepted it — exactly the silently-mismatched
        // schema-vs-parser footgun the in-line comments warn against
        // (a kept-in-lock-step parser whose schema was kept partially
        // in lock-step, dropping the ref leg).
        mcp_servers: {
          type: "array",
          items: {
            type: "object",
            anyOf: [
              {
                required: ["ref"],
                properties: {
                  ref: { type: "string", minLength: 1 },
                },
              },
              {
                required: ["name", "url"],
                properties: {
                  name: { type: "string", minLength: 1 },
                  url: { type: "string", minLength: 1 },
                  headers: {
                    type: "object",
                    additionalProperties: { type: "string" },
                  },
                  allowed_tools: { type: "array", items: { type: "string" } },
                },
              },
            ],
          },
        },
        required_signoffs: {
          type: "array",
          items: {
            type: "object",
            // @deliberate: public_key is mandatory and non-empty: a present-but-
            // malformed sign-off entry is a misconfigured security gate.
            // Rejecting it here (and erroring in the parser) prevents the
            // gate from silently disabling itself — a load-bearing safety
            // property. A dropped entry would weaken enforcement.
            required: ["public_key"],
            properties: {
              public_key: { type: "string", minLength: 1 },
              path: { type: "string" },
            },
          },
        },
        max_signoff_attempts: {
          type: "integer",
          minimum: 0,
          default: 3,
        },
      },
    },
  },
  // @deliberate: open schema — author-declared extension attributes used
  // purely for inter-node dataflow (cycle communication, source-bound state
  // pulls) are admitted. The executor reads only the keys it knows; unknown
  // keys flow through untouched.
  additionalProperties: true,
} as const;

/**
 * Tags claude-agent may include on its settling terminal verdict
 * (Success / Error / Park `tags` field).
 *
 * The base set is empty: rate-limit handling uses the `Park` terminal,
 * not tags. A deployment (or a derivative image) declares the tags its
 * agents will emit via the `RIMSKY_EXECUTOR_DECLARED_TAGS` env var
 * (comma-separated; whitespace trimmed; empty segments dropped). The
 * resolved list is advertised via `Capabilities.declared_tags`
 * (server.ts + observability.ts), so rimsky's registration-time
 * `subscribes:` `when: "<tag>" in payload.tags` cross-check sees the
 * names without a source fork. Per concept:terminal-tag tags can only
 * ride on the settling terminal verdict — no mid-dispatch emission.
 *
 * Resolved at call time (not module-load) so a test (or a late env-var
 * set) is reflected without re-importing the module — mirrors the lazy
 * `process.env` reads elsewhere in this executor (e.g.
 * `stubModeEnabled()`).
 *
 * @concept: terminal-tag
 */
export function resolveDeclaredTags(): string[] {
  const raw = process.env.RIMSKY_EXECUTOR_DECLARED_TAGS;
  if (!raw) return [];
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

/**
 * Hierarchical error-class vocabulary claude-agent advertises via
 * `Capabilities.declared_error_classes`. Operator templates' `error_types:`
 * keys range-check against this list; entries ending in `*` are prefix
 * patterns. Per concept:signal (rimsky spec 2026-05-23).
 */
export const declaredErrorClasses: string[] = [
  "agent/blocked",
  "agent/internal_error",
  "agent/attribute_invalid",
  "agent/schema_violation",
  "agent/cli_spawn_failed",
  "agent/timeout",
  "agent/subprocess_exit/*",
  "agent/rate_limited",
  "agent/context_exceeded",
  "agent/tool_use_failed/*",
  "agent/refused",
  "agent/signoff_unobtained",
];

/**
 * Serialize the schema to bytes for the Capabilities response.
 */
export function expectedAttributesSchemaBytes(): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(expectedAttributesSchema));
}
