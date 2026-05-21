import { describe, it, expect } from "vitest";
import pino from "pino";
import { SessionTokenRegistry } from "../state/session-tokens.js";
import { handleRelease } from "./release.js";
import { handleCommit } from "./commit.js";
import { handleAbandon } from "./abandon.js";

const logger = pino({ level: "silent" });

describe("release/commit/abandon", () => {
  it("release revokes the bound session token", async () => {
    const tokens = new SessionTokenRegistry();
    const tok = tokens.issue({ passId: "p_1", claimHandleId: "c_x", issuedAt: 0 });
    await handleRelease({ claim_id: "c_x" }, tokens, logger);
    expect(tokens.validate(tok)).toBeNull();
  });

  it("commit revokes the bound session token", async () => {
    const tokens = new SessionTokenRegistry();
    const tok = tokens.issue({ passId: "p_1", claimHandleId: "c_x", issuedAt: 0 });
    const r = await handleCommit({ claim_id: "c_x" }, tokens, logger);
    expect(r.ok).toBe(true);
    expect(tokens.validate(tok)).toBeNull();
  });

  it("commit is safe to call when no token is bound to the claim", async () => {
    const tokens = new SessionTokenRegistry();
    const r = await handleCommit({ claim_id: "c_never" }, tokens, logger);
    expect(r.ok).toBe(true);
  });

  it("abandon revokes the bound token (no longer a no-op)", async () => {
    const tokens = new SessionTokenRegistry();
    const tok = tokens.issue({ passId: "p_1", claimHandleId: "c_x", issuedAt: 0 });
    expect(tokens.validate(tok)).not.toBeNull();
    const r = await handleAbandon({ claim_id: "c_x" }, tokens, logger);
    expect(r.ok).toBe(true);
    expect(tokens.validate(tok)).toBeNull();
  });

  it("abandon is safe to call when no token is bound to the claim", async () => {
    const tokens = new SessionTokenRegistry();
    const r = await handleAbandon({ claim_id: "c_never" }, tokens, logger);
    expect(r.ok).toBe(true);
  });
});
