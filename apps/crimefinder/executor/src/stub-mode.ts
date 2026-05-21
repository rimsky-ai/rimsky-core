import { z } from "zod";
import type { Logger } from "pino";
import type { AgentOutcome } from "./agent-types.js";

export const StubTerminalSchema = z.discriminatedUnion("variant", [
  z.object({
    variant: z.literal("success"),
    attributes_delta: z.record(z.unknown()).optional(),
    changed: z.boolean().optional(),
    change_summary: z.string().nullable().optional(),
  }),
  z.object({
    variant: z.literal("error"),
    error_class: z.string(),
    payload: z.unknown().optional(),
  }),
  z.object({
    variant: z.literal("park"),
    reason: z.string(),
    reason_note: z.string().optional(),
    resume_at: z.string().optional(),
  }),
]);
export type StubTerminal = z.infer<typeof StubTerminalSchema>;

export const StubOutcomeSchema = z.object({
  gates_to_call: z.array(z.object({ name: z.string(), input: z.unknown() })),
  terminal: StubTerminalSchema,
  delay_ms: z.number().int().nonnegative().optional(),
});
export type StubOutcome = z.infer<typeof StubOutcomeSchema>;

export interface RunStubAgentArgs {
  userdata: Record<string, unknown>;
  dispatch: (toolName: string, input: unknown) => Promise<unknown>;
  logger: Logger;
}

const DEFAULT_OUTCOME: StubOutcome = {
  gates_to_call: [],
  terminal: { variant: "success", attributes_delta: { stub: true } },
  delay_ms: 50,
};

export async function runStubAgent(args: RunStubAgentArgs): Promise<{ outcome: AgentOutcome }> {
  let outcome: StubOutcome;
  if (args.userdata.stub_outcome === undefined) {
    outcome = DEFAULT_OUTCOME;
  } else {
    outcome = StubOutcomeSchema.parse(args.userdata.stub_outcome);
  }
  if (outcome.delay_ms) {
    await new Promise((r) => setTimeout(r, outcome.delay_ms));
  }
  for (const call of outcome.gates_to_call) {
    try {
      await args.dispatch(call.name, call.input);
    } catch (e) {
      args.logger.warn({ tool: call.name, err: String(e) }, "stub_gate_dispatch_failed");
    }
  }
  switch (outcome.terminal.variant) {
    case "success":
      return {
        outcome: {
          events: [],
          variant: "success",
          attributesDelta: outcome.terminal.attributes_delta,
          changed: outcome.terminal.changed,
          changeSummary: outcome.terminal.change_summary ?? null,
        },
      };
    case "error":
      return {
        outcome: {
          events: [],
          variant: "error",
          errorClass: outcome.terminal.error_class as never,
          payload: outcome.terminal.payload
            ? new TextEncoder().encode(JSON.stringify(outcome.terminal.payload))
            : undefined,
        },
      };
    case "park":
      return {
        outcome: {
          events: [],
          variant: "park",
          reason: outcome.terminal.reason,
          reasonNote: outcome.terminal.reason_note,
          resumeAt: outcome.terminal.resume_at,
        },
      };
  }
}
