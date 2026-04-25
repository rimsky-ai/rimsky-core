#!/usr/bin/env node
// pino ships CJS; reach the callable through the interop namespace.
import * as pinoNs from "pino";
type PinoFn = (opts?: object) => import("pino").Logger;
const pino: PinoFn = (((pinoNs as unknown) as { default?: PinoFn }).default ??
  ((pinoNs as unknown) as PinoFn));
import { startGrpcServer } from "./server.js";
import { startHttpBridge } from "./http-bridge.js";
import { startInternalMcpServer } from "./internal-mcp-server.js";

/**
 * Executable entry point for the claude-agent executor.
 *
 * Configuration via env vars (see README). Binds:
 *   - internal MCP callback on 127.0.0.1:<random-port> (advertised to subprocess)
 *   - gRPC NodeExecutor on RIMSKY_EXECUTOR_HOST:RIMSKY_EXECUTOR_PORT_GRPC
 *   - HTTP+JSON bridge on RIMSKY_EXECUTOR_HOST:RIMSKY_EXECUTOR_PORT_HTTP
 */
async function main(): Promise<void> {
  const logger = pino({ name: "claude-agent-executor" });
  const host = process.env.RIMSKY_EXECUTOR_HOST ?? "0.0.0.0";
  const grpcPort = parseInt(
    process.env.RIMSKY_EXECUTOR_PORT_GRPC ?? "7071",
    10,
  );
  const httpPort = parseInt(
    process.env.RIMSKY_EXECUTOR_PORT_HTTP ?? "7072",
    10,
  );
  const callbackHost =
    process.env.RIMSKY_EXECUTOR_CALLBACK_HOST ?? "127.0.0.1";
  const silenceTimeoutMs = parseInt(
    process.env.RIMSKY_EXECUTOR_SILENCE_MS ?? "120000",
    10,
  );

  const callback = await startInternalMcpServer({
    host: callbackHost,
    port: 0,
    logger,
  });
  logger.info({ callback_url: callback.url }, "internal MCP listening");

  const grpc = await startGrpcServer({
    host,
    port: grpcPort,
    callback,
    silenceTimeoutMs,
    logger,
  });

  const http = await startHttpBridge({
    host,
    port: httpPort,
    callback,
    silenceTimeoutMs,
    logger,
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
