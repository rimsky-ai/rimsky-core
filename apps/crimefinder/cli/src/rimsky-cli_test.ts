import { describe, it, expect } from "vitest";
import { RimskyCli } from "./rimsky-cli.js";

describe("RimskyCli", () => {
  it("templateRegister parses JSON output", async () => {
    const cli = new RimskyCli({
      exec: async () => ({ stdout: '{"template_hash":"sha-x"}', stderr: "" }),
    });
    const r = await cli.templateRegister("/tmp/x.yml");
    expect(r.template_hash).toBe("sha-x");
  });

  it("instanceCreate passes args correctly and parses output", async () => {
    let seenArgs: string[] = [];
    const cli = new RimskyCli({
      exec: async (_p, args) => {
        seenArgs = args;
        return { stdout: '{"instance_id":"i-1"}', stderr: "" };
      },
    });
    const r = await cli.instanceCreate("sha-x", { repo_root: "/r", mission: "m" });
    expect(r.instance_id).toBe("i-1");
    expect(seenArgs).toContain("--template");
    expect(seenArgs).toContain("sha-x");
  });
});
