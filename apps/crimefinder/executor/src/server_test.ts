import { describe, it, expect } from "vitest";
import { outcomeToCallbackBody, buildCallbackUrl } from "./server.js";
import { encodeAddress } from "@crimefinder/shared";

describe("outcomeToCallbackBody", () => {
  it("maps success variant", () => {
    const body = outcomeToCallbackBody({
      events: [],
      variant: "success",
      attributesDelta: { x: 1 },
      changed: true,
    });
    expect(body.success).toBeTruthy();
    expect(body.error).toBeUndefined();
    expect(body.park).toBeUndefined();
  });

  it("maps error variant", () => {
    const body = outcomeToCallbackBody({
      events: [],
      variant: "error",
      errorClass: "silence_timeout",
    });
    expect(body.error).toBeTruthy();
    expect(body.success).toBeUndefined();
  });

  it("maps park variant", () => {
    const body = outcomeToCallbackBody({
      events: [],
      variant: "park",
      reason: "time_wait",
    });
    expect(body.park).toBeTruthy();
  });

  it("includes events at the top level (not nested in outcome)", () => {
    const body = outcomeToCallbackBody({
      events: [
        {
          event: "finding_emitted",
          pass_id: "p_1",
          zone_id: "z_1",
          session_id: "s",
          ts: "2026-05-19T12:00:00.000+00:00",
          data: { x: 1 },
        },
      ],
      variant: "success",
    });
    expect(body.events).toBeInstanceOf(Array);
    expect((body.events as { name: string }[])[0].name).toBe("finding_emitted");
  });
});

describe("buildCallbackUrl", () => {
  it("appends the per-async path with encoded ack id", () => {
    expect(buildCallbackUrl("http://supervisor:9100", "ack/123")).toBe(
      "http://supervisor:9100/v1/callback/ack%2F123",
    );
  });

  it("trims trailing slashes", () => {
    expect(buildCallbackUrl("http://x/", "a")).toBe("http://x/v1/callback/a");
  });
});

// Quick sanity: the AgentRunArgs.stores carries decoded addresses; if you ever
// move encoding, ensure shared.encodeAddress still produces bytes runAgent can decode.
describe("scope address round-trip integration", () => {
  it("encodes pass-state address", () => {
    const b = encodeAddress({
      kind: "pass-state",
      pass_id: "p_1",
      state_endpoint_url: "x",
      session_token: "t",
    });
    expect(b.length).toBeGreaterThan(0);
  });
});
