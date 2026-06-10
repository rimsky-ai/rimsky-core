#!/usr/bin/env node
// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// pino ships CJS; reach the callable through the interop namespace.
import * as pinoNs from "pino";
type PinoFn = (opts?: object) => import("pino").Logger;
const pino: PinoFn = (((pinoNs as unknown) as { default?: PinoFn }).default ??
  ((pinoNs as unknown) as PinoFn));
import { startGrpcServer } from "./server.js";
import { startHttpBridge } from "./http-bridge.js";
import { startInternalMcpServer } from "./internal-mcp-server.js";
import type { CliAuthConfig } from "./cli-env.js";
import { stubModeEnabled } from "./agent-run.js";
import { Observability } from "./observability.js";
import { registerCrashHandlers } from "./crash-handlers.js";
import { loadCatalogFromEnv, parsePolicy } from "./mcp-catalog.js";
import { createClaudeCliRunner } from "./cli-runner.js";

/**
 * Executable entry point for the claude-agent executor.
 *
 * Configuration via env vars (see README). Binds:
 *   - internal MCP callback on 127.0.0.1:<random-port> (advertised to subprocess)
 *   - gRPC Executor on RIMSKY_EXECUTOR_HOST:RIMSKY_EXECUTOR_PORT_GRPC
 *   - HTTP+JSON bridge on RIMSKY_EXECUTOR_HOST:RIMSKY_EXECUTOR_PORT_HTTP
 */
async function main(): Promise<void> {
  const logger = pino({ name: "claude-agent-executor" });
  // Registered before any server starts so a crash during startup (or
  // during a server's lifetime) is logged and surfaced as a non-zero
  // exit instead of a silent vanish.
  registerCrashHandlers(logger);
  const host = process.env.RIMSKY_EXECUTOR_HOST ?? "0.0.0.0";
  const grpcPort = parseInt(
    process.env.RIMSKY_EXECUTOR_PORT_GRPC ?? "9090",
    10,
  );
  const httpPort = parseInt(
    process.env.RIMSKY_EXECUTOR_PORT_HTTP ?? "9190",
    10,
  );
  const callbackHost =
    process.env.RIMSKY_EXECUTOR_CALLBACK_HOST ?? "127.0.0.1";
  const silenceTimeoutMs = parseInt(
    process.env.RIMSKY_EXECUTOR_SILENCE_MS ?? "120000",
    10,
  );
  // README documents `RIMSKY_EXECUTOR_CLAUDE_BINARY` as the path to the
  // Claude CLI. Read once at startup and thread through to both transports
  // so a deployment can override the bare `claude` PATH lookup — required
  // for cross-stack tests that bind a stub CLI replacing the third-party
  // binary while keeping the rest of the dispatch path real. Empty leaves
  // the default ("claude" from PATH).
  const cliBinaryPath = process.env.RIMSKY_EXECUTOR_CLAUDE_BINARY ?? "";

  const cliAuth: CliAuthConfig = {
    anthropicApiKey: process.env.ANTHROPIC_API_KEY ?? "",
    claudeCodeOauthToken: process.env.CLAUDE_CODE_OAUTH_TOKEN ?? "",
  };
  if (
    !stubModeEnabled() &&
    !cliAuth.anthropicApiKey &&
    !cliAuth.claudeCodeOauthToken
  ) {
    // eslint-disable-next-line no-console
    console.error(
      "fatal: at least one of ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN must be set",
    );
    process.exit(1);
  }
  logger.info(
    {
      auth_mode: cliAuth.anthropicApiKey
        ? "api_key"
        : cliAuth.claudeCodeOauthToken
        ? "oauth"
        : "stub",
    },
    "cli auth resolved",
  );

  // Startup MCP-server catalog + allow_inline policy
  // (S-executors-mcp-catalog-transports). Parsed ONCE here and threaded into
  // both transports so every dispatch's `cli.mcp_servers` `{ ref: }` resolves
  // against the same catalog. A malformed catalog throws and fails startup
  // loudly (a dropped catalog server is a silently-unwired reference).
  const mcpCatalog = loadCatalogFromEnv(
    process.env.RIMSKY_EXECUTOR_MCP_CATALOG,
  );
  const mcpAllowInline = parsePolicy(
    process.env.RIMSKY_EXECUTOR_MCP_ALLOW_INLINE,
  );
  logger.info(
    {
      catalog_servers: Object.keys(mcpCatalog).length,
      allow_inline: mcpAllowInline,
    },
    "mcp catalog loaded",
  );

  const callback = await startInternalMcpServer({
    host: callbackHost,
    port: 0,
    logger,
  });
  logger.info({ callback_url: callback.url }, "internal MCP listening");

  // Single ledger shared between gRPC + HTTP transports so dashboards
  // can fetch traces by supervisor's dispatch_id regardless of which
  // path delivered the dispatch.
  const observability = new Observability();
  const observabilityHttpBridgeUrl =
    process.env.RIMSKY_EXECUTOR_OBSERVABILITY_HTTP_BRIDGE_URL ?? "";

  // In stub mode `runAgent` short-circuits before spawning the CLI, so the
  // runner is never reached and we leave both transports to lazily build
  // their default. In real mode, when `RIMSKY_EXECUTOR_CLAUDE_BINARY` is
  // set, build the runner here once and inject the same instance into both
  // transports so the override applies uniformly across the gRPC and HTTP
  // paths (a stub-CLI binding skewed across transports would defeat the
  // override and leave a real-CLI dispatch latent on one path).
  const sharedCliRunner =
    !stubModeEnabled() && cliBinaryPath !== ""
      ? createClaudeCliRunner({ auth: cliAuth, binaryPath: cliBinaryPath })
      : undefined;
  if (sharedCliRunner) {
    logger.info(
      { cli_binary_path: cliBinaryPath },
      "cli binary override active",
    );
  }

  const grpc = await startGrpcServer({
    host,
    port: grpcPort,
    callback,
    cliAuth,
    silenceTimeoutMs,
    logger,
    observability,
    observabilityHttpBridgeUrl,
    mcpCatalog,
    mcpAllowInline,
    cliRunner: sharedCliRunner,
  });

  const http = await startHttpBridge({
    host,
    port: httpPort,
    callback,
    cliAuth,
    silenceTimeoutMs,
    logger,
    observability,
    observabilityHttpBridgeUrl,
    mcpCatalog,
    mcpAllowInline,
    cliRunner: sharedCliRunner,
  });

  const shutdown = async (): Promise<void> => {
    logger.info("shutting down");
    await Promise.allSettled([
      grpc.shutdown(),
      http.shutdown(),
      callback.close(),
    ]);
    process.exit(0);
  };
  process.on("SIGINT", () => void shutdown());
  process.on("SIGTERM", () => void shutdown());
}

main().catch((e) => {
  // eslint-disable-next-line no-console
  console.error("fatal:", e);
  process.exit(1);
});
