import type { ReviewFindingInput, FindingClass } from "@crimefinder/shared";
import { decodeClass } from "@crimefinder/shared";
import type { GateContext } from "./types.js";
import { event } from "./types.js";

export interface ReviewFindingResult {
  finding_id: string;
  effective_class: FindingClass;
  auto_rerouted: boolean;
  // Both auto-reroute (class-5b) and tension-confirmation can fire on the
  // same call; return an array so neither signal overwrites the other. The
  // append-finding handler currently short-circuits on tension_confirmation
  // before the class-5b check, so in practice the array has 0 or 1 entries,
  // but the shape leaves room for both.
  crimefinder_error_classes?: Array<
    "concept_citation_missing" | "tension_already_cataloged"
  >;
}

export async function reviewFinding(
  input: ReviewFindingInput,
  ctx: GateContext,
): Promise<ReviewFindingResult> {
  const r = await ctx.stateClient.appendFinding(input);
  const cls = decodeClass(r.effective_class);
  ctx.emit.emit(
    event(ctx, "finding_emitted", {
      finding_id: r.finding_id,
      effective_class: cls,
      auto_rerouted: r.auto_rerouted,
      file: input.file,
    }),
  );
  const errorClasses: Array<
    "concept_citation_missing" | "tension_already_cataloged"
  > = [];
  if (r.auto_rerouted) errorClasses.push("concept_citation_missing");
  if (r.tension_confirmation) errorClasses.push("tension_already_cataloged");
  const out: ReviewFindingResult = {
    finding_id: r.finding_id,
    effective_class: cls,
    auto_rerouted: r.auto_rerouted,
  };
  if (errorClasses.length > 0) out.crimefinder_error_classes = errorClasses;
  return out;
}
