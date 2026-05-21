import path from "node:path";
import fs from "node:fs/promises";
import {
  computeFingerprint,
  generateFindingId,
  generateRowId,
  FindingClassValue,
  ReviewFindingInputSchema,
  encodeClass,
  decodeClass,
  makeGateError,
  GateError,
} from "@crimefinder/shared";
import { readConcept } from "../concepts/parser.js";
import { shouldRerouteToClass5b } from "./class-5b-rule.js";
import { mapFileToZone } from "../zones/coverage.js";
import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";

export interface AppendFindingRequest {
  session_token: string;
  class: string; // wire-encoded
  file: string;
  line_start: number;
  line_start_present: boolean;
  line_end: number;
  line_end_present: boolean;
  symbol?: string;
  description: string;
  concept_slug?: string;
  tension_slug?: string;
  confidence: string;
}

export interface AppendFindingResponse {
  finding_id: string;
  effective_class: string;
  auto_rerouted: boolean;
  tension_confirmation: boolean;
}

async function fileExistsAt(p: string): Promise<boolean> {
  try {
    await fs.access(p);
    return true;
  } catch {
    return false;
  }
}

export async function handleAppendFinding(
  req: AppendFindingRequest,
  deps: StateHandlerDeps,
): Promise<AppendFindingResponse> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  // Per spec line 427: review_finding requires a review-zone session.
  // A token without a role is allowed (e.g. legacy/test sessions); roles
  // other than review-zone are rejected so a fix-cycle / dedup / re-review
  // session can't accidentally emit new findings.
  if (meta.role && meta.role !== "review-zone") {
    throw new GateError(
      makeGateError(
        "wrong_session_role",
        "review_finding requires a review-zone session",
        false,
        { actual_role: meta.role, required_role: "review-zone" },
      ),
    );
  }
  const passId = meta.passId;
  const sessionId = meta.claimHandleId;

  const cls = decodeClass(req.class);
  ReviewFindingInputSchema.parse({
    class: cls,
    file: req.file,
    line_start: req.line_start_present ? req.line_start : null,
    line_end: req.line_end_present ? req.line_end : null,
    symbol: req.symbol,
    description: req.description,
    concept_slug: req.concept_slug ? req.concept_slug : null,
    tension_slug: req.tension_slug ? req.tension_slug : null,
    confidence: req.confidence as "high" | "low",
  });

  // Tension-confirmation routing: if the tension slug points at an open
  // tension file, write a tension_confirmation row instead.
  if (req.tension_slug) {
    const designDocs = deps.config.design_docs;
    if (designDocs) {
      const tensionsDir = designDocs.tensions_dir;
      const tensionPath = path.join(deps.repoRoot, tensionsDir, `${req.tension_slug}.md`);
      const resolvedPath = path.join(deps.repoRoot, tensionsDir, "_resolved", `${req.tension_slug}.md`);
      const isOpenTension =
        (await fileExistsAt(tensionPath)) && !(await fileExistsAt(resolvedPath));
      if (isOpenTension) {
        const zones = deps.partitionCache.getZonePlan(passId) ?? [];
        const zone = mapFileToZone(req.file, zones) ?? { id: meta.zoneId ?? "z_unknown" };
        const id = generateRowId();
        await deps.store.appendFinding({
          kind: "tension_confirmation",
          id,
          ts: new Date().toISOString(),
          pass_id: passId,
          zone_id: zone.id,
          tension_slug: req.tension_slug,
          file: req.file,
          description: req.description,
        });
        return {
          finding_id: id,
          effective_class: encodeClass(cls),
          auto_rerouted: false,
          tension_confirmation: true,
        };
      }
    }
  }

  const fingerprint = computeFingerprint({
    file: req.file,
    symbol: req.symbol,
    description: req.description,
  });

  // Re-discovery dedup: same fingerprint already in same pass → return existing id.
  const rows = await deps.store.readFindings();
  for (const r of rows) {
    if (r.kind === "finding" && r.pass_id === passId && r.fingerprint === fingerprint) {
      return {
        finding_id: r.id,
        effective_class: encodeClass(r.effective_class as FindingClassValue),
        auto_rerouted: false,
        tension_confirmation: false,
      };
    }
  }

  // Class-5b auto-routing. If the agent cited a concept_slug that points
  // at a missing doc, we still route to 5b — citing an unknown slug is a
  // proxy for "the design itself may be wrong" (the spec text the agent
  // thought it could lean on isn't there). Bare un-cited class-1-4 stays
  // intact; only the cited-but-missing case reroutes.
  let effectiveClass: FindingClassValue = cls;
  let autoRerouted = false;
  if ((cls === 1 || cls === 2 || cls === 3 || cls === 4) && req.concept_slug) {
    const designDocs = deps.config.design_docs;
    if (designDocs) {
      const conceptPath = path.join(deps.repoRoot, designDocs.concepts_dir, `${req.concept_slug}.md`);
      try {
        const doc = await readConcept(conceptPath);
        if (
          shouldRerouteToClass5b({
            description: req.description,
            conceptBoundaries: doc.boundaries,
            conceptInvariants: doc.invariants,
          })
        ) {
          effectiveClass = "5b";
          autoRerouted = true;
        }
      } catch (e) {
        deps.logger.warn(
          { concept_slug: req.concept_slug, err: String(e) },
          "concept_doc_missing_for_5b_check",
        );
        // Treat "cited slug doesn't exist" as a 5b reroute too.
        effectiveClass = "5b";
        autoRerouted = true;
      }
    }
  }

  // Zone: prefer the session's bound zoneId; fall back to file→zone mapping.
  const zones = deps.partitionCache.getZonePlan(passId) ?? [];
  const fileZone = mapFileToZone(req.file, zones);
  const zoneId = fileZone?.id ?? meta.zoneId ?? "z_unknown";
  const originatingZoneId =
    meta.zoneId && fileZone && fileZone.id !== meta.zoneId ? meta.zoneId : null;

  const id = generateFindingId();
  await deps.store.appendFinding({
    kind: "finding",
    id,
    ts: new Date().toISOString(),
    pass_id: passId,
    zone_id: zoneId,
    session_id: sessionId,
    class: cls,
    effective_class: effectiveClass,
    auto_rerouted: autoRerouted,
    file: req.file,
    line_start: req.line_start_present ? req.line_start : null,
    line_end: req.line_end_present ? req.line_end : null,
    symbol: req.symbol || undefined,
    description: req.description,
    fingerprint,
    concept_slug: req.concept_slug ? req.concept_slug : null,
    tension_slug: req.tension_slug ? req.tension_slug : null,
    confidence: req.confidence === "low" ? "low" : "high",
    status: "open",
    originating_zone_id: originatingZoneId,
  });

  return {
    finding_id: id,
    effective_class: encodeClass(effectiveClass),
    auto_rerouted: autoRerouted,
    tension_confirmation: false,
  };
}
