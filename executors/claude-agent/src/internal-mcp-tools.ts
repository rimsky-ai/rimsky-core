// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { z } from "zod";
import {
  ATTRIBUTES_TOOL_DEFINITIONS,
  AttributesReadInput,
  AttributesSetInput,
} from "./attributes-tools.js";

/**
 * Input schemas + descriptors for the MCP tools exposed to agentic
 * subprocesses on the internal callback endpoint.
 *
 * Spec: docs/specs/2026-04-25-stores-redesign-design.md §12, §16.1.
 *
 * Tool surface:
 *   - `report_complete` — terminal success. Optional `attributes_delta`
 *     for the terminal-final writeback pattern (empty for the
 *     incremental-via-callback pattern). The legacy `result` field has
 *     been retired.
 *   - `report_blocked` — emits a StreamClose `Error{error_class:
 *     "executor_blocked"}` outcome on the wire (post-E.2 the pre-rename
 *     Blocked variant collapsed into Error with the reserved
 *     `executor_blocked` class).
 *   - `report_error`   — emits a StreamClose `Error{error_class}` outcome
 *     on the wire.
 *   - `attributes_read` / `attributes_set` — per spec §12.5; defined in
 *     `attributes-tools.ts` and re-exported here.
 */
export const ReportCompleteInput = z.object({
  token: z.string(),
  /**
   * Optional terminal-final writeback. When omitted, the executor used
   * the incremental-via-callback pattern (one or more `attributes_set`
   * calls during the run).
   */
  attributes_delta: z.record(z.unknown()).optional(),
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

// Allowed snake_case ParkReason values: the closed two-value set
// per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §ParkReason collapse. Mirrors proto:executor.proto::ParkReason.
export const PARK_REASONS = [
  "await_callback",
  "snooze",
] as const;

export const ReportParkInput = z.object({
  token: z.string(),
  reason: z.enum(PARK_REASONS),
  reason_note: z.string().optional(),
  resume_at: z.string().optional(),
});

export { AttributesReadInput, AttributesSetInput };

export interface ToolDefinition {
  name: string;
  description: string;
  inputSchema: object;
}

export const TOOL_DEFINITIONS: ToolDefinition[] = [
  {
    name: "report_complete",
    description:
      "Report successful completion. Optional attributes_delta carries " +
      "the terminal-final attribute writeback; omit when the run used " +
      "incremental attributes_set calls.",
    inputSchema: {
      type: "object",
      required: ["token", "changed"],
      properties: {
        token: { type: "string" },
        attributes_delta: { type: "object" },
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
  {
    name: "report_park",
    description:
      "Park the dispatch. The supervisor pauses the node until resume_at " +
      "elapses or an invalidate wakes it. Per 2026-05-14 Piece 2, reason " +
      "is the typed ParkReason value (snake_case).",
    inputSchema: {
      type: "object",
      required: ["token", "reason"],
      properties: {
        token: { type: "string" },
        reason: {
          type: "string",
          enum: [...PARK_REASONS],
          description: "Typed park reason. The agent must pick one.",
        },
        reason_note: {
          type: "string",
          description: "Optional human-readable annotation.",
        },
        resume_at: {
          type: "string",
          format: "date-time",
          description: "Optional ISO 8601 timestamp at which to wake. Absent means signal-only.",
        },
      },
    },
  },
  ...ATTRIBUTES_TOOL_DEFINITIONS,
];
