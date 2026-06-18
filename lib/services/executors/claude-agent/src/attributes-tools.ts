// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { z } from "zod";

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

export function buildAttributesWritebackUrl(
  base: string,
  runId: string,
): string {
  const trimmed = base.replace(/\/+$/, "");
  return `${trimmed}/v1/runs/${encodeURIComponent(runId)}/attributes`;
}
