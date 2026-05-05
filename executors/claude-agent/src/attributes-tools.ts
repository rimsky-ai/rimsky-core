// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { z } from "zod";

/**
 * MCP tool wrappers exposing read / set semantics over the dispatch's
 * `attributes` object. These replace the legacy `report_complete`-with-result
 * surface (the result field has been retired per spec §12.2).
 *
 * Spec: docs/specs/2026-04-25-stores-redesign-design.md §16.1, §12.5.
 *
 * Wiring (see `internal-mcp-server.ts`):
 *   - `attributes_read` returns the dispatch-time `attributes` object as the
 *     executor saw it on spawn (read-only snapshot — does not refresh from
 *     the supervisor).
 *   - `attributes_set` POSTs `{delta: {...}}` to
 *     `${callbackUrl}/v1/attributes/${nodeId}` with the supervisor-issued
 *     cancel-token in the `Authorization` header (per spec §12.5).
 *
 * The internal-MCP server still validates `token` for both tools. Only the
 * registered run can read or set its own attributes.
 */

export const AttributesReadInput = z.object({
  token: z.string(),
});

export const AttributesSetInput = z.object({
  token: z.string(),
  delta: z.record(z.unknown()),
});

export interface AttributesToolDefinition {
  name: string;
  description: string;
  inputSchema: object;
}

export const ATTRIBUTES_TOOL_DEFINITIONS: AttributesToolDefinition[] = [
  {
    name: "attributes_read",
    description:
      "Read the per-run attributes object as captured at executor spawn. " +
      "Returns the same snapshot for the duration of the run.",
    inputSchema: {
      type: "object",
      required: ["token"],
      properties: {
        token: { type: "string" },
      },
    },
  },
  {
    name: "attributes_set",
    description:
      "Persist attribute writes to the supervisor via the incremental " +
      "writeback callback. Body is shaped {delta: {field: value, ...}}; the " +
      "supervisor merges into rimsky_node_attributes.data and persists.",
    inputSchema: {
      type: "object",
      required: ["token", "delta"],
      properties: {
        token: { type: "string" },
        delta: { type: "object" },
      },
    },
  },
];

/**
 * HTTP function used by the set tool to deliver the writeback. Tests swap
 * this out to avoid real network calls.
 */
export type PostAttributesFn = (
  url: string,
  body: { delta: Record<string, unknown> },
  cancelToken: string,
) => Promise<{ status: number }>;

export const defaultPostAttributes: PostAttributesFn = async (
  url,
  body,
  cancelToken,
) => {
  const res = await fetch(url, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${cancelToken}`,
    },
    body: JSON.stringify(body),
  });
  return { status: res.status };
};

/**
 * Build the writeback URL: `${base}/v1/attributes/${nodeId}` with `nodeId`
 * URL-encoded. Trims trailing slashes from the base.
 */
export function buildAttributesWritebackUrl(
  base: string,
  nodeId: string,
): string {
  const trimmed = base.replace(/\/+$/, "");
  return `${trimmed}/v1/attributes/${encodeURIComponent(nodeId)}`;
}
