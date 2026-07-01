#!/usr/bin/env node
// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

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

async function main(): Promise<void> {
  const logger = pino({ name: "claude-agent-executor" });
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
  const silenceTimeoutMsDefault = parseInt(
    process.env.RIMSKY_EXECUTOR_SILENCE_MS ?? "0",
    10,
  );
  const toolUseTimeoutMsDefault = parseInt(
    process.env.RIMSKY_EXECUTOR_TOOL_USE_TIMEOUT_MS ?? "0",
    10,
  );
  const cliBinaryPath = process.env.RIMSKY_EXECUTOR_CLAUDE_BINARY ?? "";
  const exposeEnvNames = (process.env.RIMSKY_CLAUDE_AGENT_EXPOSE_ENV ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);

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

  const observability = new Observability();
  const observabilityHttpBridgeUrl =
    process.env.RIMSKY_EXECUTOR_OBSERVABILITY_HTTP_BRIDGE_URL ?? "";

  const sharedCliRunner =
    !stubModeEnabled() && cliBinaryPath !== ""
      ? createClaudeCliRunner({
          auth: cliAuth,
          binaryPath: cliBinaryPath,
          exposeEnvNames,
        })
      : undefined;
  if (sharedCliRunner) {
    logger.info(
      { cli_binary_path: cliBinaryPath, expose_env_names: exposeEnvNames },
      "cli binary override active",
    );
  }

  const grpc = await startGrpcServer({
    host,
    port: grpcPort,
    callback,
    cliAuth,
    silenceTimeoutMsDefault,
    toolUseTimeoutMsDefault,
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
    silenceTimeoutMsDefault,
    toolUseTimeoutMsDefault,
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
