import * as grpc from "@grpc/grpc-js";
import type { Logger } from "pino";
import { GateError, makeGateError, GateErrorClass, encodeClass } from "@crimefinder/shared";
import { loadExecutorProtos } from "./proto-loader.js";
import type { ReviewFindingInput } from "@crimefinder/shared";

export interface StateClientConfig {
  endpoint: string;
  sessionToken: string;
  logger: Logger;
}

export interface AppendFindingResponse {
  finding_id: string;
  effective_class: string;
  auto_rerouted: boolean;
  tension_confirmation: boolean;
}

export interface RunTestsResponse {
  exit_code: number;
  output_excerpt: string;
  ran_at: string;
  cached: boolean;
}

function mapGrpcError(e: grpc.ServiceError): Error {
  const meta = e.metadata;
  const cls = meta?.get("crimefinder-error-class")?.[0]?.toString();
  if (cls) {
    const retryableStr = meta?.get("crimefinder-retryable")?.[0]?.toString();
    const extras = meta?.get("crimefinder-extras")?.[0]?.toString();
    let extrasObj: Record<string, unknown> = {};
    try {
      if (extras) extrasObj = JSON.parse(extras);
    } catch {
      // ignore
    }
    return new GateError(
      makeGateError(cls as GateErrorClass, e.message, retryableStr === "true", extrasObj),
    );
  }
  return e;
}

function unary<TReq, TRes>(
  client: grpc.Client,
  rpc: string,
  req: TReq,
): Promise<TRes> {
  return new Promise((resolve, reject) => {
    (client as unknown as Record<string, (r: TReq, cb: (e: grpc.ServiceError | null, res: TRes) => void) => void>)[
      rpc
    ](req, (err, res) => {
      if (err) return reject(mapGrpcError(err));
      resolve(res);
    });
  });
}

export class StateClient {
  private readonly client: grpc.Client;
  private readonly token: string;

  constructor(cfg: StateClientConfig) {
    const pkg = loadExecutorProtos();
    this.client = new pkg.crimefinder.v1.CrimefinderState(
      cfg.endpoint,
      grpc.credentials.createInsecure(),
    );
    this.token = cfg.sessionToken;
    void cfg.logger;
  }

  async appendFinding(input: ReviewFindingInput): Promise<AppendFindingResponse> {
    return unary(this.client, "AppendFinding", {
      session_token: this.token,
      class: encodeClass(input.class),
      file: input.file,
      line_start: input.line_start ?? 0,
      line_start_present: input.line_start !== null && input.line_start !== undefined,
      line_end: input.line_end ?? 0,
      line_end_present: input.line_end !== null && input.line_end !== undefined,
      symbol: input.symbol ?? "",
      description: input.description,
      concept_slug: input.concept_slug ?? "",
      tension_slug: input.tension_slug ?? "",
      confidence: input.confidence,
    });
  }

  async queryFindings(args: {
    pass_id?: string;
    zone_id?: string;
    status_filter?: string;
  }): Promise<{ findings: unknown[] }> {
    const r = await unary<unknown, { findings_json: Uint8Array }>(this.client, "QueryFindings", {
      session_token: this.token,
      pass_id: args.pass_id ?? "",
      zone_id: args.zone_id ?? "",
      status_filter: args.status_filter ?? "",
    });
    return { findings: JSON.parse(new TextDecoder().decode(r.findings_json)) };
  }

  async updateFindingStatus(args: {
    finding_id: string;
    status: string;
    reason?: string;
    note?: string;
    duplicate_of?: string;
  }): Promise<{ success: boolean }> {
    return unary(this.client, "UpdateFindingStatus", {
      session_token: this.token,
      finding_id: args.finding_id,
      status: args.status,
      reason: args.reason ?? "",
      note: args.note ?? "",
      duplicate_of: args.duplicate_of ?? "",
    });
  }

  async appendCoverage(args: { files_read: string[] }): Promise<{ recorded_count: number }> {
    return unary(this.client, "AppendCoverage", {
      session_token: this.token,
      files_read: args.files_read,
    });
  }

  async runTests(): Promise<RunTestsResponse> {
    return unary(this.client, "RunTests", { session_token: this.token });
  }

  async commitFix(args: {
    finding_id: string;
    fix_description: string;
    commit_message: string;
  }): Promise<{ commit_sha: string; finding_status: "fixed" }> {
    return unary(this.client, "CommitFix", { session_token: this.token, ...args });
  }

  async deferFinding(args: {
    finding_id: string;
    reason: string;
  }): Promise<{ finding_id: string; finding_status: "deferred" }> {
    return unary(this.client, "DeferFinding", { session_token: this.token, ...args });
  }

  async skipZone(args: { reason: string }): Promise<{ zone_id: string; skipped: true }> {
    return unary(this.client, "SkipZone", { session_token: this.token, ...args });
  }

  async requestHelp(args: {
    question: string;
    blocker_finding_id?: string;
  }): Promise<{ help_id: string }> {
    return unary(this.client, "RequestHelp", {
      session_token: this.token,
      question: args.question,
      blocker_finding_id: args.blocker_finding_id ?? "",
    });
  }

  async aggregateFindings(args: { pass_id: string }): Promise<{
    class_1_4_remaining: number;
    class_5: unknown[];
    dedup_file_groups: Array<{ file: string; finding_ids: string[] }>;
  }> {
    const r = await unary<unknown, {
      class_1_4_remaining: number;
      class_5_json: Uint8Array;
      dedup_file_groups_json: Uint8Array;
    }>(this.client, "AggregateFindings", {
      session_token: this.token,
      pass_id: args.pass_id,
    });
    return {
      class_1_4_remaining: r.class_1_4_remaining,
      class_5: JSON.parse(new TextDecoder().decode(r.class_5_json)),
      dedup_file_groups: JSON.parse(new TextDecoder().decode(r.dedup_file_groups_json)),
    };
  }

  async getZoneCoverage(): Promise<{
    zone_id: string;
    zone_file_count: number;
    files_covered: number;
    coverage_pct: number;
    skip_recorded: boolean;
    pass_complete: boolean;
    pass_summary: Record<string, unknown> | null;
  }> {
    const r = await unary<unknown, {
      zone_id: string;
      zone_file_count: number;
      files_covered: number;
      coverage_pct: number;
      skip_recorded: boolean;
      pass_complete: boolean;
      pass_summary_json: Uint8Array;
    }>(this.client, "GetZoneCoverage", { session_token: this.token });
    let passSummary: Record<string, unknown> | null = null;
    if (r.pass_summary_json && r.pass_summary_json.length > 0) {
      try {
        passSummary = JSON.parse(new TextDecoder().decode(r.pass_summary_json));
      } catch {
        passSummary = null;
      }
    }
    return {
      zone_id: r.zone_id,
      zone_file_count: r.zone_file_count,
      files_covered: r.files_covered,
      coverage_pct: r.coverage_pct,
      skip_recorded: r.skip_recorded,
      pass_complete: r.pass_complete,
      pass_summary: passSummary,
    };
  }

  async getReviewContext(args: { assigned_finding_ids?: string[] } = {}): Promise<unknown> {
    const r = await unary<unknown, { context_json: Uint8Array }>(
      this.client,
      "GetReviewContext",
      {
        session_token: this.token,
        assigned_finding_ids: (args.assigned_finding_ids ?? []).join(","),
      },
    );
    return JSON.parse(new TextDecoder().decode(r.context_json));
  }

  async markDuplicate(args: {
    finding_id: string;
    duplicate_of: string;
  }): Promise<{ success: boolean; skipped_due_to_conflict?: boolean }> {
    return unary(this.client, "MarkDuplicate", { session_token: this.token, ...args });
  }

  close(): void {
    this.client.close();
  }
}
