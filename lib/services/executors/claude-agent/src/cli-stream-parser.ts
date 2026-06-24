// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

export type CliProgressEvent =
  | { kind: "tool_use_start"; id: string; name: string }
  | { kind: "tool_use_end"; id: string };

export interface CliStreamParser {
  push(chunk: string): CliProgressEvent[];
}

export function makeCliStreamParser(): CliStreamParser {
  let buf = "";
  return {
    push(chunk: string): CliProgressEvent[] {
      buf += chunk;
      const events: CliProgressEvent[] = [];
      let nl = buf.indexOf("\n");
      while (nl >= 0) {
        const line = buf.slice(0, nl);
        buf = buf.slice(nl + 1);
        const trimmed = line.trim();
        if (trimmed.length > 0) {
          extractEvents(trimmed, events);
        }
        nl = buf.indexOf("\n");
      }
      return events;
    },
  };
}

function extractEvents(line: string, out: CliProgressEvent[]): void {
  let obj: unknown;
  try {
    obj = JSON.parse(line);
  } catch {
    return;
  }
  if (obj === null || typeof obj !== "object") return;
  const rec = obj as Record<string, unknown>;
  const t = rec.type;
  if (t === "assistant") {
    const msg = rec.message as Record<string, unknown> | undefined;
    const content = msg?.content;
    if (Array.isArray(content)) {
      for (const blk of content) {
        if (blk && typeof blk === "object") {
          const b = blk as Record<string, unknown>;
          if (b.type === "tool_use" && typeof b.id === "string") {
            const name = typeof b.name === "string" ? b.name : "";
            out.push({ kind: "tool_use_start", id: b.id, name });
          }
        }
      }
    }
  } else if (t === "user") {
    const msg = rec.message as Record<string, unknown> | undefined;
    const content = msg?.content;
    if (Array.isArray(content)) {
      for (const blk of content) {
        if (blk && typeof blk === "object") {
          const b = blk as Record<string, unknown>;
          if (b.type === "tool_result" && typeof b.tool_use_id === "string") {
            out.push({ kind: "tool_use_end", id: b.tool_use_id });
          }
        }
      }
    }
  }
}
