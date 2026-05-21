import { describe, it, expect } from "vitest";
import { buildCapabilitiesResponse } from "./capabilities.js";

describe("buildCapabilitiesResponse", () => {
  it("advertises sync writes and the optional mix-ins", () => {
    const c = buildCapabilitiesResponse();
    expect(c.write_semantics_allowed).toEqual(["WRITE_SEMANTICS_SYNC"]);
    expect(c.supports_split_scope).toBe(true);
    expect(c.supports_scopes_conflict).toBe(true);
    expect(c.protocols).toEqual(["claim_producer"]);
  });
});
