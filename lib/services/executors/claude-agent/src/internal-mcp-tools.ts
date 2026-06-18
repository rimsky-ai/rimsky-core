// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { z } from "zod";
import {
  ATTRIBUTES_TOOL_DEFINITIONS,
  AttributesReadInput,
  AttributesSetInput,
} from "./attributes-tools.js";

export const ReportCompleteInput = z.object({
  token: z.string(),
  attributes_delta: z.record(z.unknown()).optional(),
  changed: z.boolean(),
  change_summary: z.string().nullable().optional(),
  signoffs: z.array(z.string()).optional(),
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
        signoffs: {
          type: "array",
          items: { type: "string" },
          description:
            "base64 Ed25519 sign-off signatures, when the node requires sign-offs",
        },
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
