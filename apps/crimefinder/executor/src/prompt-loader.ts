import type { Logger } from "pino";

export interface ResolvedPrompts {
  systemPrompt: string;
  userPrompt: string;
}

export interface PromptLoaderInput {
  mission: string;
  systemPromptFromUserdata?: string;
  userPromptTemplateFromUserdata?: string;
}

const FALLBACK_PROMPTS: Record<string, ResolvedPrompts> = {
  "review-zone": {
    systemPrompt:
      "You are crimefinder's review-zone agent. Your rich prompt belongs in the template's " +
      "userdata.system_prompt; this is a safety-net fallback. Call review_context to learn your " +
      "zone, then emit findings via review_finding and report files read via review_coverage. " +
      "Call review_complete when done.",
    userPrompt: "Call review_context to begin.",
  },
  "fix-cycle": {
    systemPrompt:
      "You are crimefinder's fix-cycle agent. Your rich prompt belongs in the template's " +
      "userdata.system_prompt; this is a safety-net fallback. Call review_context to load your " +
      "assigned findings. Edit files, then run review_run_tests, then review_commit_fix per " +
      "finding. Defer if you cannot fix.",
    userPrompt: "Call review_context to begin.",
  },
  "re-review": {
    systemPrompt:
      "You are crimefinder's re-review agent. Your rich prompt belongs in the template's " +
      "userdata.system_prompt. Re-survey the affected zone, focusing on regressions from fixes.",
    userPrompt: "Call review_context to begin.",
  },
  dedup: {
    systemPrompt:
      "You are crimefinder's dedup agent. Your rich prompt belongs in the template's " +
      "userdata.system_prompt. Identify duplicate findings in your assigned file groups.",
    userPrompt: "Call review_context to begin.",
  },
};

export class UnknownMissionError extends Error {
  constructor(mission: string) {
    super(`unknown mission: ${mission}`);
    this.name = "UnknownMissionError";
  }
}

export function loadPrompts(input: PromptLoaderInput, logger: Logger): ResolvedPrompts {
  const sys = input.systemPromptFromUserdata?.trim() ?? "";
  const usr = input.userPromptTemplateFromUserdata?.trim() ?? "";
  if (sys && usr) return { systemPrompt: sys, userPrompt: usr };

  const missing = !sys && !usr ? "both" : !sys ? "system" : "user";
  logger.warn(
    { event: "prompt_missing_from_userdata", mission: input.mission, missing },
    "prompt_loader_fallback",
  );
  const fallback = FALLBACK_PROMPTS[input.mission];
  if (!fallback) {
    throw new UnknownMissionError(input.mission);
  }
  return {
    systemPrompt: sys || fallback.systemPrompt,
    userPrompt: usr || fallback.userPrompt,
  };
}
