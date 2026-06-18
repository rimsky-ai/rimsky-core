// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

export type ClassifiedAgentError = { errorClass: string } | null;

export function classifyAgentError(
  stderr: string,
  exitCode: number | null,
): ClassifiedAgentError {
  void exitCode;
  if (!stderr) return null;
  const lower = stderr.toLowerCase();

  if (
    lower.includes("context_length_exceeded") ||
    lower.includes("context window") ||
    lower.includes("context_window") ||
    lower.includes("maximum context") ||
    lower.includes("prompt is too long")
  ) {
    return { errorClass: "agent/context_exceeded" };
  }

  if (lower.includes("tool_use_failed") || lower.includes("tool execution failed")) {
    const tool = parseToolName(stderr) ?? "unknown";
    return { errorClass: `agent/tool_use_failed/${tool}` };
  }

  if (
    lower.includes("(refusal)") ||
    lower.includes("refused by the model") ||
    lower.includes("declined to respond") ||
    /\brefusal\b/.test(lower)
  ) {
    return { errorClass: "agent/refused" };
  }

  return null;
}

function parseToolName(stderr: string): string | null {
  const quoted = /tool\s+["'`]([^"'`]+)["'`]/i.exec(stderr);
  if (quoted && quoted[1]!.length > 0) return quoted[1]!;

  const colon = /tool_use_failed[:\s]+["'`]?([A-Za-z0-9_./-]+)["'`]?/i.exec(stderr);
  if (colon && colon[1]!.length > 0 && colon[1]!.toLowerCase() !== "tool") {
    return colon[1]!;
  }

  const bare = /\btool\s+([A-Za-z0-9_./-]+)\s+(?:returned|failed|errored)/i.exec(stderr);
  if (bare && bare[1]!.length > 0) return bare[1]!;

  return null;
}
