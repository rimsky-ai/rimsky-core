import * as grpc from "@grpc/grpc-js";
import type { Logger } from "pino";

import { loadProducerProtos } from "./proto-loader.js";
import { JsonlStore } from "./jsonl-store.js";
import { SessionTokenRegistry } from "./state/session-tokens.js";
import { IterationCounter } from "./state/iteration-counter.js";
import { TestCache } from "./state/test-cache.js";
import { TestRunMutex } from "./state/run-tests.js";
import { CommitMutex } from "./state/commit-mutex.js";
import { createGitOps } from "./git-ops.js";
import { readConfig } from "./config.js";
import { createPartitionCache, OpenContext } from "./scopes/types.js";
import { handleOpen, OpenRequest } from "./claim-producer/open.js";
import { handleCommit } from "./claim-producer/commit.js";
import { handleAbandon } from "./claim-producer/abandon.js";
import { handleRelease } from "./claim-producer/release.js";
import { buildCapabilitiesResponse } from "./capabilities.js";
import { splitScope } from "./claim-producer/split-scope.js";
import { scopesConflict } from "./claim-producer/scopes-conflict.js";
import { runStartupRecovery } from "./recovery/startup-scan.js";
import { GateError } from "@crimefinder/shared";
import { StateHandlerDeps, UnauthenticatedError } from "./state/handler-deps.js";

import { handleAppendFinding } from "./state/append-finding.js";
import { handleQueryFindings } from "./state/query-findings.js";
import { handleUpdateFindingStatus } from "./state/update-status.js";
import { handleAppendCoverage } from "./state/append-coverage.js";
import { handleRunTests } from "./state/run-tests-handler.js";
import { handleCommitFix } from "./state/commit-fix.js";
import { handleDeferFinding } from "./state/defer-finding.js";
import { handleSkipZone } from "./state/skip-zone.js";
import { handleRequestHelp } from "./state/request-help.js";
import { handleAggregateFindings } from "./state/aggregate-findings.js";
import { handleGetZoneCoverage } from "./state/get-zone-coverage.js";
import { handleGetReviewContext } from "./state/get-review-context.js";
import { handleMarkDuplicate } from "./state/mark-duplicate.js";

export interface ServerConfig {
  host: string;
  port: number;
  repoRoot: string;
  stateEndpointUrl: string;
  logger: Logger;
}

export interface RunningServer {
  address: string;
  shutdown(): Promise<void>;
}

// Wrap a typed handler so gRPC errors carry the error_class as metadata
// and map UnauthenticatedError → UNAUTHENTICATED.
function wrapHandler<TReq, TRes>(
  fn: (req: TReq, deps: StateHandlerDeps) => Promise<TRes>,
  deps: StateHandlerDeps,
): grpc.handleUnaryCall<TReq, TRes> {
  return (call, callback) => {
    fn(call.request as TReq, deps)
      .then((res) => callback(null, res))
      .catch((err) => {
        if (err instanceof UnauthenticatedError) {
          callback({
            code: grpc.status.UNAUTHENTICATED,
            message: err.message,
          });
          return;
        }
        if (err instanceof GateError) {
          const meta = new grpc.Metadata();
          meta.set("crimefinder-error-class", err.envelope.data.crimefinder_error_class);
          meta.set("crimefinder-retryable", String(err.envelope.data.retryable));
          meta.set("crimefinder-extras", JSON.stringify(err.envelope.data));
          callback({
            code: grpc.status.FAILED_PRECONDITION,
            message: err.envelope.message,
            metadata: meta,
          });
          return;
        }
        callback({ code: grpc.status.INTERNAL, message: String(err?.message ?? err) });
      });
  };
}

function makeOpenContext(
  ctxBase: Omit<OpenContext, "selector" | "claimId">,
  selector: string,
  claimId: string,
): OpenContext {
  return { ...ctxBase, selector, claimId };
}

export interface StartGrpcServerOptions extends ServerConfig {
  // Allow harness/tests to inject a shared token registry + partition
  // cache so they can issue tokens recognized by the in-process gates.
  tokens?: SessionTokenRegistry;
  partitionCache?: ReturnType<typeof createPartitionCache>;
}

export async function startGrpcServer(cfg: StartGrpcServerOptions): Promise<RunningServer> {
  const pkg = loadProducerProtos();
  const store = new JsonlStore({ repoRoot: cfg.repoRoot, logger: cfg.logger });
  await store.ensureDir();
  const storeDir = await store.getStoreDir();
  const tokens =
    cfg.tokens ??
    new SessionTokenRegistry({ storeDir });
  // Rehydrate any session-tokens persisted before a crash. Tokens older
  // than the TTL or with a tombstone are dropped silently.
  await tokens.reload();
  const iterCounter = new IterationCounter(store, cfg.logger);
  const testCache = new TestCache();
  const testRunMutex = new TestRunMutex();
  const commitMutex = new CommitMutex();
  const git = createGitOps();
  const config = await readConfig(cfg.repoRoot);
  const partitionCache = cfg.partitionCache ?? createPartitionCache();

  // Recovery rehydrates the same partitionCache so post-restart Open /
  // SplitScope calls see the zone plans the original pass computed.
  await runStartupRecovery({
    store,
    git,
    iterCounter,
    partitionCache,
    repoRoot: cfg.repoRoot,
    logger: cfg.logger,
  });

  const ctxBase: Omit<OpenContext, "selector" | "claimId"> = {
    repoRoot: cfg.repoRoot,
    store,
    tokens,
    iterCounter,
    stateEndpointUrl: cfg.stateEndpointUrl,
    partitionCache,
    config,
    git,
    logger: cfg.logger,
  };

  const stateDeps: StateHandlerDeps = {
    store,
    tokens,
    iterCounter,
    testCache,
    testRunMutex,
    commitMutex,
    git,
    config,
    partitionCache,
    repoRoot: cfg.repoRoot,
    logger: cfg.logger,
  };

  const server = new grpc.Server();

  type UnaryCall<TReq, TRes> = (
    call: grpc.ServerUnaryCall<TReq, TRes>,
    cb: grpc.sendUnaryData<TRes>,
  ) => void;

  // ----- ClaimProducer -----
  server.addService(pkg.rimsky.v1.ClaimProducer.service, {
    Capabilities: ((_call, cb) => cb(null, buildCapabilitiesResponse())) as UnaryCall<unknown, unknown>,
    Open: ((call, cb) => {
      const req = call.request as OpenRequest;
      handleOpen(req, makeOpenContext(ctxBase, req.selector ?? "", req.claim_id))
        .then((r) => {
          if (r.type === "acquired") {
            cb(null, {
              acquired: {
                address: Buffer.from(r.address),
                payload: Buffer.from(r.payload),
                scope: Buffer.from(r.scope),
                realized_write_semantics: r.realized_write_semantics,
              },
            });
          } else {
            cb(null, { unavailable: { message: r.message } });
          }
        })
        .catch((e) => cb({ code: grpc.status.INTERNAL, message: String(e) }));
    }) as UnaryCall<OpenRequest, unknown>,
    Commit: ((call, cb) => {
      handleCommit(
        { claim_id: (call.request as { claim_id: string }).claim_id },
        tokens,
        cfg.logger,
      )
        .then((r) => cb(null, r))
        .catch((e) => cb({ code: grpc.status.INTERNAL, message: String(e) }));
    }) as UnaryCall<unknown, unknown>,
    Abandon: ((call, cb) => {
      handleAbandon(
        { claim_id: (call.request as { claim_id: string }).claim_id },
        tokens,
        cfg.logger,
      )
        .then((r) => cb(null, r))
        .catch((e) => cb({ code: grpc.status.INTERNAL, message: String(e) }));
    }) as UnaryCall<unknown, unknown>,
    Release: ((call, cb) => {
      handleRelease(
        { claim_id: (call.request as { claim_id: string }).claim_id },
        tokens,
        cfg.logger,
      )
        .then((r) => cb(null, r))
        .catch((e) => cb({ code: grpc.status.INTERNAL, message: String(e) }));
    }) as UnaryCall<unknown, unknown>,
    SplitScope: ((call, cb) => {
      const req = call.request as {
        parent_claim_handle_id: string;
        parent_scope?: Uint8Array;
        partition_request: Uint8Array;
      };
      splitScope({
        parentClaimHandleId: req.parent_claim_handle_id,
        parentScope: req.parent_scope ?? new Uint8Array(),
        partitionRequest: req.partition_request ?? new Uint8Array(),
        ctx: makeOpenContext(ctxBase, "", req.parent_claim_handle_id),
      })
        .then((subs) =>
          cb(null, {
            sub_scopes: subs.map((s) => ({
              scope_data: Buffer.from(s.scopeData),
              partition_key: s.partitionKey,
              producer_metadata: Buffer.from(s.producerMetadata),
            })),
          }),
        )
        .catch((e) => cb({ code: grpc.status.INTERNAL, message: String(e) }));
    }) as UnaryCall<unknown, unknown>,
    ScopesConflict: ((call, cb) => {
      const req = call.request as { scope_a: Uint8Array; scope_b: Uint8Array };
      cb(null, { conflict: scopesConflict(req.scope_a, req.scope_b) });
    }) as UnaryCall<unknown, unknown>,
  });

  // ----- CrimefinderState -----
  server.addService(pkg.crimefinder.v1.CrimefinderState.service, {
    AppendFinding: wrapHandler(handleAppendFinding, stateDeps),
    QueryFindings: wrapHandler(handleQueryFindings, stateDeps),
    UpdateFindingStatus: wrapHandler(handleUpdateFindingStatus, stateDeps),
    AppendCoverage: wrapHandler(handleAppendCoverage, stateDeps),
    RunTests: wrapHandler(handleRunTests, stateDeps),
    CommitFix: wrapHandler(handleCommitFix, stateDeps),
    DeferFinding: wrapHandler(handleDeferFinding, stateDeps),
    SkipZone: wrapHandler(handleSkipZone, stateDeps),
    RequestHelp: wrapHandler(handleRequestHelp, stateDeps),
    AggregateFindings: wrapHandler(handleAggregateFindings, stateDeps),
    GetZoneCoverage: wrapHandler(handleGetZoneCoverage, stateDeps),
    GetReviewContext: wrapHandler(handleGetReviewContext, stateDeps),
    MarkDuplicate: wrapHandler(handleMarkDuplicate, stateDeps),
  });

  const port = await new Promise<number>((resolve, reject) => {
    server.bindAsync(`${cfg.host}:${cfg.port}`, grpc.ServerCredentials.createInsecure(), (err, p) => {
      if (err) return reject(err);
      resolve(p);
    });
  });

  return {
    address: `${cfg.host}:${port}`,
    async shutdown() {
      await new Promise<void>((resolve) => server.tryShutdown(() => resolve()));
    },
  };
}
