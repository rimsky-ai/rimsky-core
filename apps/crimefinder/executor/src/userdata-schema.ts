import { NAMED_EVENT_NAMES } from "@crimefinder/shared";

// JSON Schema (draft-07) for executor userdata.
// `system_prompt` and `user_prompt_template` match claude-agent so the
// template's `source_file:` resolutions land in the right fields; the
// prompt-loader and agent-run both read these names directly.
export const userdataSchema = {
  $schema: "http://json-schema.org/draft-07/schema#",
  type: "object",
  required: ["mission"],
  properties: {
    mission: {
      type: "string",
      enum: ["review-zone", "fix-cycle", "dedup", "re-review"],
    },
    system_prompt: { type: "string" },
    user_prompt_template: { type: "string" },
    stub_outcome: { type: "object" },
    model: { type: "string" },
    max_turns: { type: "integer", minimum: 1 },
    // Coverage knobs: pushed by the producer at dispatch time (read from
    // cfg:coverage in .crimefinder/config.yml). The executor falls back to
    // cold-defaults (80%, require_skip) only if the producer didn't supply.
    coverage_threshold_pct: { type: "number", minimum: 0, maximum: 100 },
    coverage_on_below_threshold: {
      type: "string",
      enum: ["require_skip", "warn", "allow"],
    },
    // Note: `iter_num` and `assigned_finding_ids` are NOT carried in
    // userdata. Rimsky does not substitute `{{...}}` inside userdata
    // (runtime/userdata_overrides.go deep-merge-only; spec invariant 11),
    // so per-child values must travel on the source-tree-zone address
    // (see shared/scope-addresses.ts and producer/claim-producer/split-scope.ts).
    // The pass trigger: manual / cron / webhook / concept_edit_watch.
    // Carried through for observability — gates do not read it.
    trigger: {
      type: "string",
      enum: ["manual", "cron", "webhook", "concept_edit_watch"],
    },
  },
  additionalProperties: true,
} as const;

export function userdataSchemaBytes(): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(userdataSchema));
}

export const declaredEvents: string[] = [...NAMED_EVENT_NAMES];
