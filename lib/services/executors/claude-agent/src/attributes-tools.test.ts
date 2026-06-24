// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import {
  AttributesReadInput,
  ATTRIBUTES_TOOL_DEFINITIONS,
} from "./attributes-tools.js";

describe("attributes-tool input schemas", () => {
  it("AttributesReadInput parses minimal payload", () => {
    expect(AttributesReadInput.parse({ token: "tok" }).token).toBe("tok");
    expect(() => AttributesReadInput.parse({})).toThrow();
  });

  it("ATTRIBUTES_TOOL_DEFINITIONS exposes only read (set was retired)", () => {
    const names = ATTRIBUTES_TOOL_DEFINITIONS.map((t) => t.name).sort();
    expect(names).toEqual(["attributes_read"]);
  });
});
