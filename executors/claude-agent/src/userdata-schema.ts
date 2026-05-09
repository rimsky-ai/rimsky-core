// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

/**
 * userdata-schema.ts — JSON Schema for claude-agent userdata.
 *
 * Drives two paths:
 *   1. Returned in `Capabilities.userdata_schema` so rimsky can validate
 *      template userdata at registration and at dispatch.
 *   2. Used internally for input validation when claude-agent receives
 *      a dispatch (defence in depth — rimsky already validated, but the
 *      executor re-checks before launching the CLI).
 *
 * Per @blessed-invariant 11 the rimsky-side validation never inspects
 * fragment values outside the schema-validation pass. The executor's
 * own re-check is in-process; both are read-only.
 */
// Schema property names match parseCliConfig in server.ts /
// http-bridge.ts: snake_case. Mismatched names (camelCase here +
// snake_case in the parser) would cause templates that use the
// real key names to fail schema validation, while templates using
// camelCase would pass validation but be silently ignored at run
// time. The parser is the source of truth; schema follows it.
export const userdataSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  type: "object",
  properties: {
    // cwd_from_store / cwd are top-level userdata fields read by
    // server.ts and http-bridge.ts to resolve the agent's working
    // directory. Templates that legitimately set either must pass
    // schema validation; without these declarations the
    // top-level `additionalProperties: false` would reject them.
    cwd_from_store: { type: "string" },
    cwd: { type: "string" },
    // model / system_prompt / user_prompt_template are read at the
    // top level of userdata by runAndCallback in server.ts and
    // runAndCallback in http-bridge.ts. They are NOT under `cli.*`
    // even though they affect the spawned CLI. Keeping them at the
    // schema's top level matches the parser; nesting them under
    // `cli` would break dispatch-time validation against the
    // executor's advertised Capabilities.userdata_schema.
    model: { type: "string" },
    system_prompt: { type: "string" },
    user_prompt_template: { type: "string" },
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
  additionalProperties: false,
} as const;

/**
 * Names of events claude-agent may emit via the NamedEvent wire type.
 * Initially empty — rate-limit handling uses ParkRequested, not events.
 * Update this when J12 or future work adds emission points.
 */
export const declaredEvents: string[] = [];

/**
 * Serialize the schema to bytes for the Capabilities response.
 */
export function userdataSchemaBytes(): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(userdataSchema));
}
