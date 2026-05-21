import { makeGateError, GateError } from "@crimefinder/shared";
import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";
import { runTests } from "./run-tests.js";

export interface RunTestsHandlerRequest {
  session_token: string;
}

export interface RunTestsHandlerResponse {
  exit_code: number;
  output_excerpt: string;
  ran_at: string;
  cached: boolean;
}

const EXCERPT_CAP = 4096;

function buildExcerpt(stdout: string, stderr: string): string {
  const out = stdout + "\n---STDERR---\n" + stderr;
  if (out.length <= EXCERPT_CAP) return out;
  return out.slice(out.length - EXCERPT_CAP);
}

export async function handleRunTests(
  req: RunTestsHandlerRequest,
  deps: StateHandlerDeps,
): Promise<RunTestsHandlerResponse> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  // Spec line 429: review_run_tests is rejected for dedup sessions — they
  // have no business shelling out to the test runner.
  if (meta.role === "dedup") {
    throw new GateError(
      makeGateError(
        "wrong_session_role",
        "review_run_tests is not available to dedup sessions",
        false,
        { actual_role: "dedup" },
      ),
    );
  }
  if (!deps.config.tests) {
    throw new GateError(
      makeGateError("test_command_not_configured", "config.tests is missing", false),
    );
  }
  // Snapshot the prior cache entry's ranAt so we can decide whether the
  // call below hit the cache. runTests makes the real cache decision
  // under its own mutex; we only need to compare ranAt before vs after.
  const beforeRanAt = deps.testCache.peek(meta.passId)?.ranAt;
  const r = await runTests(
    {
      passId: meta.passId,
      repoRoot: deps.repoRoot,
      command: deps.config.tests.command,
      timeoutMs: deps.config.tests.timeout_seconds * 1000,
    },
    {
      git: deps.git,
      cache: deps.testCache,
      mutex: deps.testRunMutex,
      logger: deps.logger,
    },
  );
  // `cached` is true iff the post-call ranAt matches the pre-call ranAt.
  const cached = beforeRanAt !== undefined && beforeRanAt === r.ranAt;
  return {
    exit_code: r.exitCode,
    output_excerpt: buildExcerpt(r.stdoutTail, r.stderrTail),
    ran_at: r.ranAt,
    cached,
  };
}
