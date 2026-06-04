// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

/**
 * Raised by `parseCliConfig` (and its helpers `parseRequiredSignoffs` /
 * `parseMcpServers`) when a config field is PRESENT but malformed in a way
 * that would weaken enforcement if silently dropped.
 *
 * The drop-silently idiom is fine for tuning fields (e.g. `add_dirs`): a
 * malformed entry there only loses a non-load-bearing hint. It is NOT fine
 * for the sign-off gate (`required_signoffs`) or its validator wiring
 * (`mcp_servers`): a dropped `required_signoffs` entry silently disables part
 * (or all) of a security gate the host configured, letting unsigned output
 * resolve to terminal success. That is precisely the silent-ungating failure
 * the gate exists to prevent.
 *
 * The two `runAndCallback` catch blocks (server.ts gRPC + http-bridge.ts
 * HTTP) recognize this error and emit a terminal `AsyncCallbackBody.error`
 * with `error_class: "agent/attribute_invalid"` — the same fail-loud failure
 * mode as the empty-`dispatch_id` path in agent-run.ts — rather than the
 * generic `agent/internal_error`.
 *
 * `agent/attribute_invalid` is one of the executor's `declaredErrorClasses`
 * (expected-attributes-schema.ts), so it is a recognized terminal class.
 */
export class CliConfigError extends Error {
  /** The declared executor error class to surface on the wire. */
  readonly errorClass = "agent/attribute_invalid";

  constructor(message: string) {
    super(message);
    this.name = "CliConfigError";
  }
}

export function isCliConfigError(e: unknown): e is CliConfigError {
  return e instanceof CliConfigError;
}
