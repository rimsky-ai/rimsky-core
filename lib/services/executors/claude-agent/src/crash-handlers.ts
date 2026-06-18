// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import type { Logger } from "pino";

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
