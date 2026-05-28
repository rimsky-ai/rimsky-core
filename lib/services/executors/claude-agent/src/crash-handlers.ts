// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import type { Logger } from "pino";

/**
 * Registers process-level crash handlers so an uncaught exception or
 * unhandled promise rejection is logged structured and exits non-zero
 * instead of vanishing into Node's default behavior.
 *
 * Without this, a peer-protocol violation from grpc-js's HTTP/2
 * internals (NGHTTP2_ERR_PROTO / code -505) bubbles up as an
 * `'error'` event with no listener, becomes an uncaught exception,
 * and the process disappears with no log line — leaving the container
 * dead while supervisor-side polling continues to think the executor
 * is alive. The orchestrator only learns the executor is gone when a
 * health check fails minutes later.
 *
 * `onFatal` is the terminal action and defaults to `process.exit(1)`.
 * Tests pass a stub so they can assert the handler fired without
 * killing the test runner.
 */
export function registerCrashHandlers(
  logger: Logger,
  onFatal: (code: number) => void = (code) => process.exit(code),
): void {
  process.on("uncaughtException", (err, origin) => {
    logger.fatal(
      {
        error: String(err),
        stack: err instanceof Error ? err.stack : undefined,
        origin,
      },
      "uncaughtException",
    );
    onFatal(1);
  });
  process.on("unhandledRejection", (reason) => {
    const err = reason instanceof Error ? reason : undefined;
    logger.fatal(
      {
        reason: String(reason),
        stack: err?.stack,
      },
      "unhandledRejection",
    );
    onFatal(1);
  });
}
