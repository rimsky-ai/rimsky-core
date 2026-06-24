// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import { TokenRegistry, dispatchContextSnapshot, type TokenEntry } from "./token-registry.js";

describe("TokenRegistry", () => {
  it("registers, looks up, and releases tokens", () => {
    const reg = new TokenRegistry();
    const entry = makeEntry("run-1");
    reg.register("tok-1", entry);
    expect(reg.size()).toBe(1);
    expect(reg.lookup("tok-1")).toBe(entry);
    reg.release("tok-1");
    expect(reg.size()).toBe(0);
    expect(reg.lookup("tok-1")).toBeUndefined();
  });

  it("lookup of unknown token returns undefined", () => {
    const reg = new TokenRegistry();
    expect(reg.lookup("nope")).toBeUndefined();
  });
});

function makeEntry(runId: string): TokenEntry {
  return {
    runId,
    attributesAtSpawn: {},
    dispatchContext: dispatchContextSnapshot("d-1", "rs-1", "", ""),
    cancelToken: "ct",
    nodeId: "n-1",
    callbackUrl: "http://supervisor.invalid/cb",
    onComplete: async () => ({ status: "accepted" as const }),
    onBlocked: async () => {},
    onError: async () => {},
  };
}
