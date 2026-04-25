import { z } from "zod";

/**
 * Input schemas for the three callback MCP tools exposed to agentic subprocesses.
 *
 * @source rimsky/src/callback-mcp/tools.ts
 *
 * These validate arguments on the server side; the JSON-schema form emitted by
 * `tools/list` below is the same shape rendered for MCP clients.
 */
export const ReportCompleteInput = z.object({
  token: z.string(),
  result: z.unknown(),
  changed: z.boolean(),
  change_summary: z.string().nullable().optional(),
});

export const ReportBlockedInput = z.object({
  token: z.string(),
  reason: z.string(),
  context: z.unknown().optional(),
});

export const ReportErrorInput = z.object({
  token: z.string(),
  error_class: z.string(),
  payload: z.unknown().optional(),
});

export interface ToolDefinition {
  name: string;
  description: string;
  inputSchema: object;
}

export const TOOL_DEFINITIONS: ToolDefinition[] = [
  {
    name: "report_complete",
    description: "Report successful completion with a structured result.",
    inputSchema: {
      type: "object",
      required: ["token", "result", "changed"],
      properties: {
        token: { type: "string" },
        result: {},
        changed: { type: "boolean" },
        change_summary: { type: ["string", "null"] },
      },
    },
  },
  {
    name: "report_blocked",
    description: "Report that work cannot continue. Treated as agent_blocked.",
    inputSchema: {
      type: "object",
      required: ["token", "reason"],
      properties: {
        token: { type: "string" },
        reason: { type: "string" },
        context: {},
      },
    },
  },
  {
    name: "report_error",
    description:
      "Report a structured failure matching the cell's declared error class.",
    inputSchema: {
      type: "object",
      required: ["token", "error_class"],
      properties: {
        token: { type: "string" },
        error_class: { type: "string" },
        payload: {},
      },
    },
  },
];
