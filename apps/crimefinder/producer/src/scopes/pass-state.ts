import crypto from "node:crypto";
import {
  PassStateAddressSchema,
  generatePassId,
  PassStartedRowSchema,
} from "@crimefinder/shared";
import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";

// `@pass-state:new&mission=...&trigger=...` — `params` flow through the
// selector because `ClaimProducer.OpenRequest` doesn't carry instance-level
// params separately. The template URL-encodes the values via
// `{{params.mission}}` substitution before this handler ever sees them.
//
// fix_cycle_cap is intentionally NOT a CLI/template parameter: the
// template hardcodes three fix-iter-N nodes, so honoring a runtime
// override would silently underrun or overrun the template. We record
// the constant 3 in pass_started for observability.
const FIXED_FIX_CYCLE_CAP = 3;

export async function openPassState(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const mission = q.mission ?? "convergence pass";
  const triggerRaw = q.trigger ?? "manual";
  const trigger = (["manual", "cron", "webhook", "concept_edit_watch"] as const).includes(
    triggerRaw as "manual",
  )
    ? (triggerRaw as "manual" | "cron" | "webhook" | "concept_edit_watch")
    : "manual";
  const passId = generatePassId();
  const templateHash = q.template_hash ?? "sha256-unspecified";
  const paramsHash =
    "sha256-" +
    crypto
      .createHash("sha256")
      .update(JSON.stringify({ pass_id: passId, mission, trigger }))
      .digest("hex");

  const passRow = PassStartedRowSchema.parse({
    kind: "pass_started",
    id: passId,
    ts: new Date().toISOString(),
    mission,
    trigger,
    template_hash: templateHash,
    fix_cycle_cap: FIXED_FIX_CYCLE_CAP,
    params_hash: paramsHash,
  });
  await ctx.store.appendPassStarted(passRow);

  const sessionToken = ctx.tokens.issue({
    passId,
    claimHandleId: ctx.claimId,
    issuedAt: Date.now(),
  });

  const address = PassStateAddressSchema.parse({
    kind: "pass-state",
    pass_id: passId,
    state_endpoint_url: ctx.stateEndpointUrl,
    session_token: sessionToken,
  });
  const addressBytes = new TextEncoder().encode(JSON.stringify(address));
  const payloadBytes = new TextEncoder().encode(JSON.stringify({ pass_id: passId }));
  const scopeBytes = new TextEncoder().encode(JSON.stringify({ kind: "pass-state", pass_id: passId }));

  ctx.logger.info({ passId, mission, trigger }, "pass_opened");
  return { address: addressBytes, payload: payloadBytes, scope: scopeBytes };
}
