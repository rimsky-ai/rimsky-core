// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { Logger } from "pino";
import { registerCrashHandlers } from "./crash-handlers.js";

/**
 * Bug 3 regression coverage: prior versions of main.ts registered only
 * SIGINT and SIGTERM handlers. An `'error'` event with no listener on
 * the gRPC server's underlying HTTP/2 transport became an uncaught
 * exception that killed the executor process silently — the container
 * died, but supervisor-side polling continued to think the executor was
 * alive until the next health check.
 */

interface CapturedLogEntry {
  level: "fatal";
  obj: object;
  msg: string;
}

function makeStubLogger(captured: CapturedLogEntry[]): Logger {
  const stub = {
    fatal: (obj: object, msg: string) => {
      captured.push({ level: "fatal", obj, msg });
    },
  };
  return stub as unknown as Logger;
}

describe("registerCrashHandlers", () => {
  // Capture and restore Node's listener state so the test runner isn't
  // poisoned by handlers we register here. We can't use the deprecated
  // NodeJS.UncaughtExceptionListener / UnhandledRejectionListener types,
  // so we type via process.listeners() which infers correctly.
  let savedException: ReturnType<typeof process.listeners<"uncaughtException">>;
  let savedRejection: ReturnType<typeof process.listeners<"unhandledRejection">>;

  beforeEach(() => {
    savedException = process.listeners("uncaughtException");
    savedRejection = process.listeners("unhandledRejection");
    process.removeAllListeners("uncaughtException");
    process.removeAllListeners("unhandledRejection");
  });

  afterEach(() => {
    process.removeAllListeners("uncaughtException");
    process.removeAllListeners("unhandledRejection");
    for (const l of savedException) process.on("uncaughtException", l);
    for (const l of savedRejection) process.on("unhandledRejection", l);
  });

  it("logs fatal + invokes onFatal(1) on uncaughtException", () => {
    const captured: CapturedLogEntry[] = [];
    const fatalCalls: number[] = [];
    registerCrashHandlers(makeStubLogger(captured), (code) => {
      fatalCalls.push(code);
    });

    process.emit("uncaughtException", new Error("simulated crash"));

    expect(fatalCalls).toEqual([1]);
    expect(captured).toHaveLength(1);
    expect(captured[0]!.msg).toBe("uncaughtException");
    const obj = captured[0]!.obj as { error: string; stack?: string };
    expect(obj.error).toContain("simulated crash");
    expect(obj.stack).toBeDefined();
  });

  it("logs fatal + invokes onFatal(1) on unhandledRejection (Error reason)", () => {
    const captured: CapturedLogEntry[] = [];
    const fatalCalls: number[] = [];
    registerCrashHandlers(makeStubLogger(captured), (code) => {
      fatalCalls.push(code);
    });

    const err = new Error("rejected promise");
    // Construct a placeholder Promise that's already settled so emitting
    // a synthetic unhandledRejection event doesn't leave a real pending
    // rejection lying around in the test process.
    const placeholderPromise = Promise.resolve();
    process.emit("unhandledRejection", err, placeholderPromise);

    expect(fatalCalls).toEqual([1]);
    expect(captured).toHaveLength(1);
    expect(captured[0]!.msg).toBe("unhandledRejection");
    const obj = captured[0]!.obj as { reason: string; stack?: string };
    expect(obj.reason).toContain("rejected promise");
    expect(obj.stack).toBeDefined();
  });

  it("logs fatal + invokes onFatal(1) on unhandledRejection (non-Error reason)", () => {
    const captured: CapturedLogEntry[] = [];
    const fatalCalls: number[] = [];
    registerCrashHandlers(makeStubLogger(captured), (code) => {
      fatalCalls.push(code);
    });

    process.emit(
      "unhandledRejection",
      "string reason",
      Promise.resolve(),
    );

    expect(fatalCalls).toEqual([1]);
    expect(captured).toHaveLength(1);
    const obj = captured[0]!.obj as { reason: string; stack?: string };
    expect(obj.reason).toBe("string reason");
    expect(obj.stack).toBeUndefined();
  });
});
