import { describe, it, expect } from "vitest";
import { makeStateDeps } from "./test-helpers.js";
import { handleUpdateFindingStatus } from "./update-status.js";

describe("handleUpdateFindingStatus", () => {
  it("appends a status_update row on a valid status", async () => {
    const { deps } = await makeStateDeps();
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    await handleUpdateFindingStatus(
      { session_token: tok, finding_id: "f_x", status: "fixing" },
      deps,
    );
    const rows = await deps.store.readFindings();
    expect(rows.some((r) => r.kind === "status_update" && r.status === "fixing")).toBe(true);
  });

  it("rejects unknown statuses", async () => {
    const { deps } = await makeStateDeps();
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    await expect(
      handleUpdateFindingStatus(
        { session_token: tok, finding_id: "f_x", status: "made-up" },
        deps,
      ),
    ).rejects.toThrow();
  });

  it("requires a reason for deferred", async () => {
    const { deps } = await makeStateDeps();
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    await expect(
      handleUpdateFindingStatus(
        { session_token: tok, finding_id: "f_x", status: "deferred" },
        deps,
      ),
    ).rejects.toThrow();
  });
});
