// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

export const expectedAttributesSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  type: "object",
  properties: {
    cwd_from_store: { type: "string", default: "" },
    cwd: { type: "string", default: "" },
    model: { type: "string", default: "claude-sonnet-4-5" },
    system_prompt: { type: "string" },
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
      properties: {
        bare: { type: "boolean" },
        allowed_tools: { type: "array", items: { type: "string" } },
        disallowed_tools: { type: "array", items: { type: "string" } },
        add_dirs: { type: "array", items: { type: "string" } },
        max_budget_usd: { type: "string" },
        permission_mode: {
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
  additionalProperties: true,
} as const;

/*
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

export function expectedAttributesSchemaBytes(): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(expectedAttributesSchema));
}
