// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

import { describe, it, expect } from "vitest";
import { makeCliStreamParser } from "./cli-stream-parser.js";

describe("makeCliStreamParser", () => {
  it("emits tool_use_start on assistant message with tool_use content block", () => {
    const p = makeCliStreamParser();
    const evs = p.push(
      JSON.stringify({
        type: "assistant",
        message: {
          content: [{ type: "tool_use", id: "toolu_1", name: "Bash", input: {} }],
        },
      }) + "\n",
    );
    expect(evs).toEqual([{ kind: "tool_use_start", id: "toolu_1", name: "Bash" }]);
  });

  it("emits tool_use_end on user message with tool_result content block", () => {
    const p = makeCliStreamParser();
    const evs = p.push(
      JSON.stringify({
        type: "user",
        message: {
          content: [{ type: "tool_result", tool_use_id: "toolu_1", content: "ok" }],
        },
      }) + "\n",
    );
    expect(evs).toEqual([{ kind: "tool_use_end", id: "toolu_1" }]);
  });

  it("buffers partial lines across chunks", () => {
    const p = makeCliStreamParser();
    const line = JSON.stringify({
      type: "assistant",
      message: { content: [{ type: "tool_use", id: "toolu_2", name: "Read" }] },
    });
    const halves = [line.slice(0, 30), line.slice(30) + "\n"];
    const a = p.push(halves[0]);
    expect(a).toEqual([]);
    const b = p.push(halves[1]);
    expect(b).toEqual([{ kind: "tool_use_start", id: "toolu_2", name: "Read" }]);
  });

  it("ignores non-JSON lines and shapes we don't care about", () => {
    const p = makeCliStreamParser();
    const evs = p.push(
      "not json\n" +
        JSON.stringify({ type: "system", subtype: "init" }) +
        "\n" +
        JSON.stringify({ type: "result", subtype: "success" }) +
        "\n",
    );
    expect(evs).toEqual([]);
  });

  it("emits multiple tool_use events from a single assistant message with parallel calls", () => {
    const p = makeCliStreamParser();
    const evs = p.push(
      JSON.stringify({
        type: "assistant",
        message: {
          content: [
            { type: "text", text: "thinking" },
            { type: "tool_use", id: "toolu_A", name: "Bash" },
            { type: "tool_use", id: "toolu_B", name: "Read" },
          ],
        },
      }) + "\n",
    );
    expect(evs).toEqual([
      { kind: "tool_use_start", id: "toolu_A", name: "Bash" },
      { kind: "tool_use_start", id: "toolu_B", name: "Read" },
    ]);
  });
});
