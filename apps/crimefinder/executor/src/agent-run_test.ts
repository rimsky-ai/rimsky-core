import { describe, it, expect } from "vitest";
import { pino } from "pino";
import { encodeAddress } from "@crimefinder/shared";
import { runAgent } from "./agent-run.js";

const logger = pino({ level: "silent" });

describe("runAgent (stub mode)", () => {
  it("returns success with default stub outcome", async () => {
    const addr = encodeAddress({
      kind: "pass-state",
      pass_id: "p_1",
      state_endpoint_url: "127.0.0.1:0",
      session_token: "tok",
    });
    const outcome = await runAgent({
      dispatchId: "d_1",
      attributes: {},
      stores: [{ alias: "pass-state", address: addr }],
      callbackUrl: "http://noop",
      silenceTimeoutMs: 10_000,
      stubMode: true,
      logger,
    });
    expect(outcome.variant).toBe("success");
  });

  it("returns error when no usable store address", async () => {
    const outcome = await runAgent({
      dispatchId: "d_1",
      attributes: {},
      stores: [],
      callbackUrl: "http://noop",
      silenceTimeoutMs: 10_000,
      stubMode: true,
      logger,
    });
    expect(outcome.variant).toBe("error");
  });

  // Spec lines 836-841: with zero source-tree-zone sub-claims (the
  // fan-out parent dispatched as a regular leaf), fix-cycle and
  // re-review missions terminate immediately with success rather than
  // spawning an agent on nothing.
  it("succeeds with no work for fix-cycle when stores are empty", async () => {
    const outcome = await runAgent({
      dispatchId: "d_1",
      attributes: { mission: "fix-cycle" },
      stores: [],
      callbackUrl: "http://noop",
      silenceTimeoutMs: 10_000,
      stubMode: true,
      logger,
    });
    expect(outcome.variant).toBe("success");
    if (outcome.variant === "success") expect(outcome.changed).toBe(false);
  });

  it("succeeds with no work for re-review when stores are empty", async () => {
    const outcome = await runAgent({
      dispatchId: "d_1",
      attributes: { mission: "re-review" },
      stores: [],
      callbackUrl: "http://noop",
      silenceTimeoutMs: 10_000,
      stubMode: true,
      logger,
    });
    expect(outcome.variant).toBe("success");
    if (outcome.variant === "success") expect(outcome.changed).toBe(false);
  });

  it("succeeds with no work for fix-cycle when only a pass-state address is dispatched", async () => {
    const addr = encodeAddress({
      kind: "pass-state",
      pass_id: "p_1",
      state_endpoint_url: "127.0.0.1:0",
      session_token: "tok",
    });
    const outcome = await runAgent({
      dispatchId: "d_1",
      attributes: { mission: "fix-cycle" },
      stores: [{ alias: "pass-state", address: addr }],
      callbackUrl: "http://noop",
      silenceTimeoutMs: 10_000,
      stubMode: true,
      logger,
    });
    expect(outcome.variant).toBe("success");
    if (outcome.variant === "success") expect(outcome.changed).toBe(false);
  });

  // Per concept:inertness (attribute values are structurally inert to
  // rimsky's substitution pass) and the read-only `address:` channel
  // for per-child data, iter_num and assigned_finding_ids ride on the
  // source-tree-zone address bytes — NOT in the attribute bag. Verify
  // the address round-trips both fields and the runner finds the zone
  // primary.
  it("threads iter_num and assigned_finding_ids from the address into the gate context", async () => {
    const addr = encodeAddress({
      kind: "source-tree-zone",
      pass_id: "p_1",
      zone_id: "z_a",
      zone_label: "src/a",
      zone_files: ["src/a/x.ts"],
      repo_root_path: "/tmp/x",
      state_endpoint_url: "127.0.0.1:0",
      session_token: "tok",
      iter_num: 2,
      assigned_finding_ids: ["f_1", "f_2"],
    });
    // Decoded address must still expose the address-bound fields.
    const { decodeAddress } = await import("@crimefinder/shared");
    const decoded = decodeAddress(addr);
    expect(decoded.kind).toBe("source-tree-zone");
    if (decoded.kind === "source-tree-zone") {
      expect(decoded.iter_num).toBe(2);
      expect(decoded.assigned_finding_ids).toEqual(["f_1", "f_2"]);
    }
    // Dispatch the address as the primary — the stub-mode runner emits
    // zone_started, proving the runner found the zone address.
    const outcome = await runAgent({
      dispatchId: "d_1",
      attributes: { mission: "fix-cycle" },
      stores: [{ alias: "source-tree", address: addr }],
      callbackUrl: "http://noop",
      silenceTimeoutMs: 10_000,
      stubMode: true,
      logger,
    });
    expect(outcome.variant).toBe("success");
    const zoneStarted = outcome.events.find((e) => e.event === "zone_started");
    expect(zoneStarted).toBeDefined();
    expect(zoneStarted!.zone_id).toBe("z_a");
  });
});
