import type { Logger } from "pino";
import { decodeAddress, ScopeAddress, NamedEventEnvelope, makeNamedEvent } from "@crimefinder/shared";
import type { AgentOutcome } from "./agent-types.js";
import { StateClient } from "./state-client.js";
import { startInternalMcpServer, DispatchFn } from "./internal-mcp-server.js";
import { McpTokenRegistry } from "./token-registry.js";
import { reviewContext } from "./gates/review-context.js";
import { reviewFinding } from "./gates/review-finding.js";
import { reviewCoverage } from "./gates/review-coverage.js";
import { reviewComplete } from "./gates/review-complete.js";
import { reviewRunTests } from "./gates/review-run-tests.js";
import { reviewCommitFix } from "./gates/review-commit-fix.js";
import { reviewDefer } from "./gates/review-defer.js";
import { reviewSkipZone } from "./gates/review-skip-zone.js";
import { reviewRequestHelp } from "./gates/review-request-help.js";
import { reviewDedupMark } from "./gates/review-dedup-mark.js";
import type { GateContext, NamedEventEmitter } from "./gates/types.js";
import { loadPrompts } from "./prompt-loader.js";
import { runStubAgent } from "./stub-mode.js";
import { createClaudeCliRunner } from "./cli-runner.js";
import { buildCliEnv, CliAuthConfig } from "./cli-env.js";
import { SilenceWatch } from "./silence-watch.js";
import { readRepoCoverage } from "./repo-config.js";

export interface DispatchedStore {
  alias: string;
  address: Uint8Array;
}

export interface AgentRunArgs {
  // dispatchId identifies this *dispatch* of the agent (one execution attempt
  // of one fan-out child). It travels into named-event `session_id` and into
  // GateContext as `sessionId`.
  dispatchId: string;
  // runId identifies the *rimsky node-run* (the node-level retry envelope
  // surrounding all dispatches that share a node). It is stamped onto the
  // bearer-token issued by the internal MCP server for diagnostic /
  // observability use — every per-Execute MCP server instance creates a
  // fresh `McpTokenRegistry` and mints a one-shot bearer (no cross-Execute
  // reuse). Conservative on purpose: each dispatch gets clean state, so a
  // crash in one MCP server can't poison the next. server.ts:Execute
  // computes runId from `req.node_id ?? randomUUID()`. When omitted here
  // we fall back to dispatchId so single-shot tests still work.
  runId?: string;
  // Post-2026-05-21 userdata collapse: the wire field is `attributes`,
  // and this executor-internal field carries the unpacked bag.
  attributes: {
    mission?: string;
    stub_outcome?: unknown;
    system_prompt?: string;
    user_prompt_template?: string;
    model?: string;
    max_turns?: number;
    // Threaded through to review_complete so the gate can enforce
    // coverage_below_threshold without re-reading config.
    coverage_threshold_pct?: number;
    coverage_on_below_threshold?: "require_skip" | "warn" | "allow";
    [k: string]: unknown;
  };
  stores: DispatchedStore[];
  callbackUrl: string;
  silenceTimeoutMs: number;
  stubMode: boolean;
  cliAuth?: CliAuthConfig;
  cliBinPath?: string; // for tests; defaults to "claude"
  allowedTools?: string[];
  logger: Logger;
}

// Addresses that carry a typed-state session (pass-state, source-tree-zone,
// dedup-batch). no-op addresses are returned by parent fan-out scopes and
// have no session_token, so we never pick them as the dispatch primary.
type DispatchableAddress = Extract<
  ScopeAddress,
  { kind: "source-tree-zone" | "pass-state" | "dedup-batch" }
>;

function isDispatchable(addr: ScopeAddress): addr is DispatchableAddress {
  return addr.kind !== "no-op";
}

function pickPrimaryAddress(stores: DispatchedStore[]): DispatchableAddress | null {
  for (const s of stores) {
    if (!s.address || s.address.length === 0) continue;
    try {
      const addr = decodeAddress(s.address);
      // Prefer source-tree-zone (zone-bound session) over others.
      if (addr.kind === "source-tree-zone") return addr;
    } catch {
      // skip
    }
  }
  for (const s of stores) {
    if (!s.address || s.address.length === 0) continue;
    try {
      const addr = decodeAddress(s.address);
      if (isDispatchable(addr)) return addr;
    } catch {
      // skip
    }
  }
  return null;
}

const DEFAULT_ALLOWED_TOOLS = [
  "Read",
  "Glob",
  "Grep",
  "Edit",
  "Write",
  "mcp__crimefinder__review_context",
  "mcp__crimefinder__review_finding",
  "mcp__crimefinder__review_coverage",
  "mcp__crimefinder__review_complete",
  "mcp__crimefinder__review_run_tests",
  "mcp__crimefinder__review_commit_fix",
  "mcp__crimefinder__review_defer",
  "mcp__crimefinder__review_skip_zone",
  "mcp__crimefinder__review_request_help",
  "mcp__crimefinder__review_dedup_mark",
];

export async function runAgent(args: AgentRunArgs): Promise<AgentOutcome> {
  const events: NamedEventEnvelope[] = [];
  const emitter: NamedEventEmitter = {
    emit(env) {
      events.push(env);
    },
  };

  const mission = (args.attributes.mission ?? "review-zone") as
    | "review-zone"
    | "fix-cycle"
    | "dedup"
    | "re-review";

  const primary = pickPrimaryAddress(args.stores);
  // Spec lines 836-841: with zero sub-scopes (e.g. empty repo, no affected
  // zones to fix or re-review), the runner dispatches the parent node as a
  // regular leaf with no source-tree-zone sub-claim. Detect this case for
  // fix-cycle / re-review missions — there's literally nothing to do —
  // and terminate immediately with success rather than firing up an agent.
  if (!primary) {
    if (mission === "fix-cycle" || mission === "re-review") {
      return { events, variant: "success", changed: false };
    }
    return { events, variant: "error", errorClass: "tool_error" };
  }
  // Same idea for fix-cycle / re-review when the only dispatchable address
  // is pass-state (no source-tree-zone child) — no zone-bound work, so
  // succeed silently instead of spawning an agent that has nothing to do.
  if ((mission === "fix-cycle" || mission === "re-review") && primary.kind === "pass-state") {
    return { events, variant: "success", changed: false };
  }
  const passId = primary.pass_id;
  const sessionToken = primary.session_token;
  const stateEndpoint = primary.state_endpoint_url;
  const zoneId = primary.kind === "source-tree-zone" ? primary.zone_id : undefined;
  const zoneLabel = primary.kind === "source-tree-zone" ? primary.zone_label : undefined;
  const zoneFiles =
    primary.kind === "source-tree-zone"
      ? primary.zone_files
      : primary.kind === "dedup-batch"
        ? primary.file_groups.map((g) => g.file)
        : undefined;
  const role = mission === "fix-cycle" ? "fix-cycle" : mission === "dedup" ? "dedup" : mission === "re-review" ? "re-review" : "review-zone";

  const stateClient = new StateClient({ endpoint: stateEndpoint, sessionToken, logger: args.logger });

  // Fix-cycle / re-review children carry `iter_num` and (fix-cycle only)
  // `assigned_finding_ids` on the source-tree-zone address. Rimsky does
  // NOT run `{{...}}` substitution inside attribute `default:` values
  // (deep-merge only — see `runtime/attribute_overrides.go`,
  // `graph/attribute/doc.go` under concept:inertness), so the per-child
  // wiring rides on the address bytes the producer's SplitScope →
  // openFanOutChild populates.
  const assignedFindingIds =
    primary.kind === "source-tree-zone" ? primary.assigned_finding_ids : undefined;
  const iterNum =
    primary.kind === "source-tree-zone" ? primary.iter_num : undefined;

  const gateCtx: GateContext = {
    stateClient,
    emit: emitter,
    passId,
    zoneId,
    sessionId: args.dispatchId,
    role,
    logger: args.logger,
    zoneLabel,
    zoneFiles,
    mission,
    assignedFindingIds,
    iterNum,
  };

  // Emit pass-lifecycle / zone-lifecycle named events at dispatch time so
  // the spec's twelve-event surface (spec lines 584-595) is actually
  // populated by every dispatched run. `pass_opened` is fired by review-zone
  // dispatches (the first agent into a pass; consumers dedupe by pass_id).
  // `zone_started` is fired by every zone-bound dispatch (review-zone,
  // fix-cycle, re-review). `pass_closed` is emitted from
  // `review-complete.ts` on the terminal review-zone completion, when
  // the producer reports `pass_complete:true` — the canonical "pass
  // complete" signal stays the `pass_finished` JSONL row written by the
  // producer's report scope.
  if (role === "review-zone") {
    emitter.emit(
      makeNamedEvent("pass_opened", {
        passId,
        sessionId: args.dispatchId,
        data: { mission },
      }),
    );
  }
  if (primary.kind === "source-tree-zone") {
    emitter.emit(
      makeNamedEvent("zone_started", {
        passId,
        zoneId,
        sessionId: args.dispatchId,
        data: {
          zone_label: zoneLabel ?? "",
          mission,
        },
      }),
    );
  }

  // Coverage threshold knobs. Priority order:
  //   1. attributes.coverage_threshold_pct / coverage_on_below_threshold,
  //      if the template forwarded them (legacy / explicit override).
  //   2. .crimefinder/config.yml under REPO_ROOT (cfg:coverage), read at
  //      dispatch time. This is the spec-aligned path — the template DSL
  //      has no clean substitution for nested cfg.* values, so the
  //      executor reads the repo's config directly.
  //   3. Hard-coded defaults matching producer/src/config.ts (80%,
  //      require_skip) when neither is available.
  const repoRoot =
    primary.kind === "source-tree-zone" ? primary.repo_root_path : process.cwd();
  const repoCoverage = await readRepoCoverage(repoRoot).catch((e) => {
    args.logger.warn({ err: String(e) }, "repo_coverage_read_failed");
    return { thresholdPct: 80, onBelow: "require_skip" as const };
  });
  const completeOpts = {
    coverageThresholdPct:
      typeof args.attributes.coverage_threshold_pct === "number"
        ? args.attributes.coverage_threshold_pct
        : repoCoverage.thresholdPct,
    coverageOnBelow:
      args.attributes.coverage_on_below_threshold === "warn" ||
      args.attributes.coverage_on_below_threshold === "allow" ||
      args.attributes.coverage_on_below_threshold === "require_skip"
        ? args.attributes.coverage_on_below_threshold
        : repoCoverage.onBelow,
  };

  const dispatch: DispatchFn = async (tool, input) => {
    switch (tool) {
      case "review_context":
        return reviewContext(input, gateCtx);
      case "review_finding":
        return reviewFinding(input as never, gateCtx);
      case "review_coverage":
        return reviewCoverage(input as never, gateCtx);
      case "review_complete":
        return reviewComplete(input, gateCtx, completeOpts);
      case "review_run_tests":
        return reviewRunTests(input, gateCtx);
      case "review_commit_fix":
        return reviewCommitFix(input as never, gateCtx);
      case "review_defer":
        return reviewDefer(input as never, gateCtx);
      case "review_skip_zone":
        return reviewSkipZone(input as never, gateCtx);
      case "review_request_help":
        return reviewRequestHelp(input as never, gateCtx);
      case "review_dedup_mark":
        return reviewDedupMark(input as never, gateCtx);
      default:
        throw new Error(`unknown tool: ${tool}`);
    }
  };

  let outcome: AgentOutcome;
  if (args.stubMode) {
    const r = await runStubAgent({
      attributes: args.attributes,
      dispatch: async (tool, input) => dispatch(tool, input),
      logger: args.logger,
    });
    outcome = { ...r.outcome, events };
  } else {
    if (!args.cliAuth) {
      stateClient.close();
      return { events, variant: "error", errorClass: "tool_error" };
    }
    const registry = new McpTokenRegistry();
    let timedOut = false;
    const silence = new SilenceWatch({
      timeoutMs: args.silenceTimeoutMs,
      onTimeout: () => {
        timedOut = true;
      },
      logger: args.logger,
    });
    // Wire silence-watch.touch() into the MCP server so every authenticated
    // tool call resets the timer — long sessions that emit no stdout but
    // are actively driving gates won't false-trigger silence_timeout (#18).
    const mcp = await startInternalMcpServer({
      logger: args.logger,
      dispatch,
      registry,
      runId: args.runId ?? args.dispatchId,
      onToolCall: () => silence.touch(),
    });
    const cliEnvResult = buildCliEnv(args.cliAuth);
    const prompts = loadPrompts(
      {
        mission,
        systemPromptFromAttributes: args.attributes.system_prompt,
        userPromptTemplateFromAttributes: args.attributes.user_prompt_template,
      },
      args.logger,
    );
    try {
      const runner = createClaudeCliRunner();
      // The bearer-token is delivered via the MCP config's headers block
      // (see internal-mcp-server.startInternalMcpServer); the CLI doesn't
      // need a separate env var to thread it.
      const env = { ...cliEnvResult.env };
      const res = await runner.spawn(
        {
          bin: args.cliBinPath ?? "claude",
          mcpConfigPath: mcp.mcpConfigPath,
          allowedTools: args.allowedTools ?? DEFAULT_ALLOWED_TOOLS,
          cwd: primary.kind === "source-tree-zone" ? primary.repo_root_path : process.cwd(),
          systemPrompt: prompts.systemPrompt,
          userPrompt: prompts.userPrompt,
          env,
          model: args.attributes.model as string | undefined,
          maxTurns: args.attributes.max_turns as number | undefined,
        },
        () => silence.touch(),
      );
      silence.stop();
      if (timedOut) {
        outcome = { events, variant: "error", errorClass: "silence_timeout" };
      } else if (res.exitCode === 0) {
        outcome = { events, variant: "success", changed: true };
      } else {
        outcome = { events, variant: "error", errorClass: "tool_error" };
      }
    } finally {
      silence.stop();
      cliEnvResult.cleanup();
      await mcp.close();
    }
  }

  stateClient.close();
  return outcome;
}
