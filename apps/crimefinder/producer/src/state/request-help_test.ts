import { describe, it, expect } from "vitest";
import { makeStateDeps } from "./test-helpers.js";
import { handleRequestHelp } from "./request-help.js";

describe("handleRequestHelp", () => {
  it("writes a help_request row and returns help_id", async () => {
    const { deps } = await makeStateDeps();
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    const r = await handleRequestHelp(
      { session_token: tok, question: "what now?", blocker_finding_id: "f_x" },
      deps,
    );
    expect(r.help_id).toBeTruthy();
    const rows = await deps.store.readFindings();
    expect(rows.some((row) => row.kind === "help_request")).toBe(true);
  });
});
