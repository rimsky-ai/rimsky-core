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
          type: "string",
          enum: ["default", "ask", "deny"],
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
 * Empty: rate-limit handling uses the `Park` terminal, not events. Reachable
 * from `Capabilities.declared_events` (server.ts + observability.ts);
 * keeping it as an exported symbol means future emission points land in
 * one place rather than scattered constants.
 */
export const declaredEvents: string[] = [];

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
];

/**
 * Serialize the schema to bytes for the Capabilities response.
 */
export function expectedAttributesSchemaBytes(): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(expectedAttributesSchema));
}
