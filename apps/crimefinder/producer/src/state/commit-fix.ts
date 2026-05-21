import {
  generateRowId,
  makeGateError,
  GateError,
} from "@crimefinder/shared";
import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";
import { materializeFindings } from "./materialize.js";
import { GitCommitError } from "../git-ops.js";

export interface CommitFixRequest {
  session_token: string;
  finding_id: string;
  fix_description: string;
  commit_message: string;
}

export interface CommitFixResponse {
  commit_sha: string;
  finding_status: "fixed";
}

function pathOverlap(changedPath: string, findingFile: string): boolean {
  if (changedPath === findingFile) return true;
  if (findingFile.endsWith("/")) return changedPath.startsWith(findingFile);
  // Treat findingFile as a directory if no extension or if a prefix match for
  // the entire path segment.
  const segment = findingFile + "/";
  return changedPath.startsWith(segment);
}

export async function handleCommitFix(
  req: CommitFixRequest,
  deps: StateHandlerDeps,
): Promise<CommitFixResponse> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  // review_commit_fix is a fix-cycle gate (per spec): only fix-cycle
  // sessions may commit fixes. Tokens without a role (legacy / unit
  // tests) are allowed; review-zone / dedup / re-review tokens are
  // rejected so a reviewer can't accidentally land a commit attributed
  // to a fix agent.
  if (meta.role && meta.role !== "fix-cycle") {
    throw new GateError(
      makeGateError(
        "wrong_session_role",
        "review_commit_fix requires a fix-cycle session",
        false,
        { actual_role: meta.role, required_role: "fix-cycle" },
      ),
    );
  }

  return deps.commitMutex.withLock(async () => {
    // 1. Validate finding exists and is open|fixing.
    const rows = await deps.store.readFindings();
    const m = materializeFindings(rows);
    const target = m.get(req.finding_id);
    if (!target) {
      throw new GateError(
        makeGateError("finding_not_found", `unknown finding ${req.finding_id}`, false, {
          finding_id: req.finding_id,
        }),
      );
    }
    if (target.status !== "open" && target.status !== "fixing") {
      throw new GateError(
        makeGateError(
          "finding_already_resolved",
          `finding ${req.finding_id} status is ${target.status}`,
          false,
          {
            finding_id: req.finding_id,
            current_status: target.status,
            resolved_at_commit: target.lastUpdate?.resolved_at_commit ?? null,
          },
        ),
      );
    }

    // 2. Working tree must be dirty.
    const status = await deps.git.status(deps.repoRoot);
    if (status.clean) {
      throw new GateError(makeGateError("working_tree_clean", "no uncommitted changes", false));
    }

    // 3. Changes must overlap finding's file scope.
    const findingFile = target.row.file;
    const matchingPaths = status.paths.filter((p) => pathOverlap(p, findingFile));
    if (matchingPaths.length === 0) {
      throw new GateError(
        makeGateError(
          "working_tree_changes_out_of_scope",
          `no changes overlap finding ${req.finding_id}'s scope (${findingFile})`,
          false,
          { finding_id: req.finding_id, finding_file: findingFile, changed_paths: status.paths },
        ),
      );
    }

    // 4. If policy requires tests, the most-recent cached test result must be
    //    fresh (mtime ≤ ranAt) AND have exited 0.
    if (deps.config.require_tests_before_commit) {
      const cached = deps.testCache.peek(meta.passId);
      const currentMtime = await deps.git.mtime(deps.repoRoot);
      if (!cached || currentMtime > cached.treeMtimeAtRun) {
        throw new GateError(
          makeGateError("tests_not_recent", "tests have not been run since the working tree changed", true),
        );
      }
      if (cached.exitCode !== 0) {
        throw new GateError(
          makeGateError("tests_failed", `cached test result exit ${cached.exitCode}`, false),
        );
      }
    }

    // 5. git add the in-scope paths.
    await deps.git.add(deps.repoRoot, matchingPaths);

    // 6. git commit with the Resolves: footer.
    const fullMsg = `${req.commit_message}\n\nResolves: ${req.finding_id}`;
    let sha: string;
    try {
      sha = await deps.git.commit(deps.repoRoot, fullMsg);
    } catch (e) {
      if (e instanceof GitCommitError) {
        throw new GateError(
          makeGateError("commit_failed", "git commit failed", false, { stderr_excerpt: e.stderr.slice(0, 2048) }),
        );
      }
      throw e;
    }

    // 7. Append status_update row inside the mutex, post-commit.
    await deps.store.appendFinding({
      kind: "status_update",
      id: generateRowId(),
      ts: new Date().toISOString(),
      ref: req.finding_id,
      status: "fixed",
      by_pass: meta.passId,
      by_session: meta.claimHandleId,
      resolved_at_commit: sha,
    });

    return { commit_sha: sha, finding_status: "fixed" };
  });
}
