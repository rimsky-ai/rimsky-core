import fs from "node:fs/promises";
import path from "node:path";
import { GateError, makeGateError } from "@crimefinder/shared";
import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";
import { mapFileToZone } from "../zones/coverage.js";

export interface AppendCoverageRequest {
  session_token: string;
  files_read: string[];
}

type FileCheck =
  | { status: "ok" }
  | { status: "escaped" }
  | { status: "missing" };

async function fileCheckUnderRoot(repoRoot: string, rel: string): Promise<FileCheck> {
  // Distinguish two failure modes:
  //   "escaped" — the resolved path leaves repoRoot ("..\\..\\..\\etc\\passwd",
  //               absolute path). Security-relevant; log/warn separately.
  //   "missing" — the resolved path is inside repoRoot but the file isn't there.
  const full = path.resolve(repoRoot, rel);
  const relAgain = path.relative(repoRoot, full);
  if (relAgain.startsWith("..") || path.isAbsolute(relAgain)) {
    return { status: "escaped" };
  }
  try {
    const st = await fs.stat(full);
    if (st.isFile()) return { status: "ok" };
    return { status: "missing" };
  } catch {
    return { status: "missing" };
  }
}

export async function handleAppendCoverage(
  req: AppendCoverageRequest,
  deps: StateHandlerDeps,
): Promise<{ recorded_count: number }> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  // Spec line 428: review_coverage is for review-zone sessions. fix-cycle,
  // dedup, and re-review sessions don't report coverage.
  if (meta.role && meta.role !== "review-zone") {
    throw new GateError(
      makeGateError(
        "wrong_session_role",
        `review_coverage requires a review-zone session (this session is ${meta.role})`,
        false,
        { actual_role: meta.role, required_role: "review-zone" },
      ),
    );
  }
  // Every file must exist under repoRoot. Escape attempts (path traversal,
  // absolute paths) are treated as a distinct, security-relevant failure
  // mode so they can be logged and surfaced separately from benign typos.
  const missing: string[] = [];
  const escaped: string[] = [];
  for (const file of req.files_read) {
    const r = await fileCheckUnderRoot(deps.repoRoot, file);
    if (r.status === "missing") missing.push(file);
    else if (r.status === "escaped") escaped.push(file);
  }
  if (escaped.length > 0) {
    // Path-traversal attempts are not a routine error. Log at warn so an
    // operator looking at the logs notices.
    deps.logger.warn(
      {
        session_id: meta.claimHandleId,
        pass_id: meta.passId,
        escaped,
      },
      "coverage_path_escape_attempt",
    );
    throw new GateError(
      makeGateError(
        "coverage_file_escaped",
        `review_coverage cited ${escaped.length} file path(s) outside the repo root`,
        false,
        { escaped },
      ),
    );
  }
  if (missing.length > 0) {
    throw new GateError(
      makeGateError(
        "coverage_file_missing",
        `review_coverage cited ${missing.length} file(s) not present under repo root`,
        false,
        { missing },
      ),
    );
  }
  const zones = deps.partitionCache.getZonePlan(meta.passId) ?? [];
  let count = 0;
  for (const file of req.files_read) {
    const zone = mapFileToZone(file, zones);
    const zoneId = zone?.id ?? meta.zoneId ?? "z_unknown";
    await deps.store.appendCoverage({
      ts: new Date().toISOString(),
      pass_id: meta.passId,
      session_id: meta.claimHandleId,
      zone_id: zoneId,
      file,
    });
    count += 1;
  }
  return { recorded_count: count };
}
