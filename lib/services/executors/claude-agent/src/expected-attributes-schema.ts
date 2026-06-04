// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

/**
 * expected-attributes-schema.ts — JSON Schema for claude-agent's
 * accepted attribute shape.
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
 */
// Schema property names match parseCliConfig in server.ts /
// http-bridge.ts: snake_case. Mismatched names (camelCase here +
// snake_case in the parser) would cause templates that use the
// real key names to fail schema validation, while templates using
// camelCase would pass validation but be silently ignored at run
// time. The parser is the source of truth; schema follows it.
export const expectedAttributesSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  type: "object",
  properties: {
    // cwd_from_store / cwd are top-level attribute fields read by
    // server.ts and http-bridge.ts to resolve the agent's working
    // directory.
    cwd_from_store: { type: "string" },
    cwd: { type: "string" },
    // model / system_prompt / user_prompt are read at the top level
    // by the dispatch entrypoint in agent-run.ts. They are NOT under
    // `cli.*` even though they affect the spawned CLI. Keeping them
    // at the schema's top level matches the dispatch entrypoint.
    model: { type: "string", default: "claude-sonnet-4-5" },
    system_prompt: { type: "string" },
    // user_prompt is the fully rimsky-resolved user prompt (post-
    // collapse the executor reads the resolved value verbatim and
    // appends a fixed metadata footer; the old `user_prompt_template`
    // two-stage substitution is gone).
    user_prompt: { type: "string" },
    cli: {
      type: "object",
      // The cli.* fields below are exactly those parseCliConfig
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
          // Real Claude CLI `--permission-mode` values. `bypassPermissions`
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
        // Sign-off gate (parsed by parseCliConfig as mcpServers /
        // requiredSignoffs / maxSignoffAttempts). Host-wired validator
        // MCP servers and the required (public_key, path) signature
        // pairs. Kept in lock-step with the parser per the comment above.
        mcp_servers: {
          type: "array",
          items: {
            type: "object",
            // name + url are mandatory and non-empty: a present-but-
            // malformed host-server entry must be rejected by rimsky at
            // registration/dispatch, not silently dropped (which would
            // unwire a validator the host intended the agent to reach).
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
        },
        required_signoffs: {
          type: "array",
          items: {
            type: "object",
            // public_key is mandatory and non-empty: a present-but-
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
  // Open schema: author-declared extension attributes used purely for
  // inter-node dataflow (cycle communication, source-bound state
  // pulls) are admitted. The executor reads only the keys it knows;
  // unknown keys flow through untouched.
  additionalProperties: true,
} as const;

/**
 * Names of events claude-agent may emit via the NamedEvent wire type.
 *
 * The base set is empty: rate-limit handling uses the `Park` terminal, not
 * events. A deployment (or a derivative image) declares the names its
 * agents will emit via the `emit_named_event` MCP tool through the
 * `RIMSKY_EXECUTOR_DECLARED_EVENTS` env var (comma-separated; whitespace
 * trimmed; empty segments dropped). The resolved list is advertised via
 * `Capabilities.declared_events` (server.ts + observability.ts), so
 * rimsky's registration-time `subscribes:` cross-check sees the names
 * without a source fork, and it is the self-consistency list the
 * `emit_named_event` handler checks an emitted name against.
 *
 * Resolved at call time (not module-load) so a test (or a late
 * env-var set) is reflected without re-importing the module — mirrors
 * the lazy `process.env` reads elsewhere in this executor
 * (e.g. `stubModeEnabled()`).
 */
export function resolveDeclaredEvents(): string[] {
  const raw = process.env.RIMSKY_EXECUTOR_DECLARED_EVENTS;
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
