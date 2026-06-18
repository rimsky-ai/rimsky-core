// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

export class CliConfigError extends Error {
  readonly errorClass = "agent/attribute_invalid";

  constructor(message: string) {
    super(message);
    this.name = "CliConfigError";
  }
}

export function isCliConfigError(e: unknown): e is CliConfigError {
  return e instanceof CliConfigError;
}
