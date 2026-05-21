import { describe, it, expect } from "vitest";
import { pino } from "pino";
import { loadPrompts, UnknownMissionError } from "./prompt-loader.js";

const logger = pino({ level: "silent" });

describe("loadPrompts", () => {
  it("returns user-supplied prompts verbatim (trimmed)", () => {
    const r = loadPrompts(
      {
        mission: "review-zone",
        systemPromptFromUserdata: "  SYS  ",
        userPromptTemplateFromUserdata: "  USR  ",
      },
      logger,
    );
    expect(r.systemPrompt).toBe("SYS");
    expect(r.userPrompt).toBe("USR");
  });

  it("falls back to bundled system when only user is supplied", () => {
    const r = loadPrompts(
      {
        mission: "fix-cycle",
        userPromptTemplateFromUserdata: "USR",
      },
      logger,
    );
    expect(r.systemPrompt).toContain("crimefinder's fix-cycle agent");
    expect(r.userPrompt).toBe("USR");
  });

  it("falls back to full bundled when both are missing", () => {
    const r = loadPrompts({ mission: "dedup" }, logger);
    expect(r.systemPrompt).toContain("crimefinder's dedup agent");
    expect(r.userPrompt.length).toBeGreaterThan(0);
  });

  it("throws UnknownMissionError on unknown mission with no userdata", () => {
    expect(() => loadPrompts({ mission: "made-up" }, logger)).toThrow(UnknownMissionError);
  });
});
