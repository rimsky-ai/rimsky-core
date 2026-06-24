// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { z } from "zod";

export const AttributesReadInput = z.object({
  token: z.string(),
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
];
