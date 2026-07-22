export const meta = {
  name: 'intent-vs-reality-gap-audit',
  description: 'Audit Rimsky sketches/specs/plans/divergences/tensions for deferred, unimplemented, or buggy work vs the actual code',
  phases: [
    { title: 'Harvest', detail: 'parallel readers extract intent claims + first-pass code check' },
    { title: 'Verify', detail: 'adversarially refute each claimed gap against real code' },
    { title: 'Synthesize', detail: 'dedup, rank by blast radius, write report' },
  ],
}

const DIV = '.ok-planner/history/plans/'
const divergences = [
  '2026-05-15-control-plane-mcp-and-auth-plan-divergences.md',
  '2026-05-17-sensor-messaging-unification-divergences.md',
  '2026-05-19-crimefinder-divergences.md',
  '2026-05-19-multi-instance-template-ergonomics-divergences.md',
  '2026-05-20-attribute-pull-resolution-divergences.md',
  '2026-05-20-multi-source-substitution-decline-divergences.md',
  '2026-05-21-attribute-overrides-matcher-overlay-divergences.md',
  '2026-05-21-userdata-collapse-into-attributes-divergences.md',
  '2026-05-22-fan-out-safety-scope-first-divergences.md',
  '2026-05-23-signal-taxonomy-and-policy-decoupling-divergences.md',
  '2026-05-24-host-agent-and-proxy-divergences.md',
  '2026-05-24-instance-debugger-divergences.md',
  '2026-05-24-repo-reorganization-divergences.md',
  '2026-05-25-concept-doc-self-containment-divergences.md',
  '2026-05-26-collapse-sdk-into-protocols-divergences.md',
  '2026-05-27-release-skill-divergences.md',
  '2026-05-27-root-folder-reorg-divergences.md',
  '2026-05-27-services-reintegration-divergences.md',
  '2026-05-28-quality-of-life-features-divergences.md',
  '2026-05-29-console-upstream-auth-audit-and-fixes-divergences.md',
  '2026-05-29-cve-remediation-v0.3.0-divergences.md',
  '2026-06-02-acceptance-coverage-recovery-divergences.md',
  '2026-06-02-rimsky-core-remediation-divergences.md',
  '2026-06-03-instance-lifecycle-durable-by-default-divergences.md',
  '2026-06-04-claude-agent-signoff-gate-divergences.md',
].map(f => DIV + f)

const SK = '.ok-planner/sketches/'
const sketches = [
  '2026-04-26-package-manager.md',
  '2026-05-07-agentic-telemetry.md',
  '2026-05-13-geo-cycle.md',
  '2026-05-14-rimsky-development-kit.md',
  '2026-05-16-full-traceability-sketch.md',
  '2026-05-23-unify-child-execution-sketch.md',
  '2026-05-28-bundled-stores-split-scope.md',
  '2026-05-28-claude-agent-protocol-coverage.md',
  '2026-05-29-events-streaming-and-breakpoint-delivery.md',
  '2026-05-29-message-schema-layer.md',
  '2026-05-29-reactive-nomenclature-rework.md',
  '2026-05-30-audit-log-durable-spool-sketch.md',
].map(f => SK + f)

const TN = '.ok-planner/design/tensions/'
const tensions = [
  'anonymous-mode-locks-out-late-bind.md',
  'retired-numbered-invariant-14.md',
  'blob-backend-conformance-fixture-asymmetry.md',
  'callback-hostname-split.md',
  'coalesced-fire-observability-gap.md',
  'compose-prefix-client-side.md',
  'control-api-version-prefix.md',
  'delegation-and-fanout-share-runtime-primitive.md',
  'event-vocabulary-implies-delivery.md',
  'events-kind-no-enum.md',
  'force-fire-204-hides-asynchrony.md',
  'frame-lookup-on-every-enqueue.md',
  'heartbeat-cutoff-asymmetry.md',
  'internal-service-auth-unspeced.md',
  'pre-v1-hash-instability.md',
  'quality-rule-custom-handler-ordering.md',
  'quality-rule-severity-string-footgun.md',
  'reaper-vs-bail-abandon-asymmetry.md',
  'serial-queue-per-instance.md',
  'sqlite-vs-memory-reject-asymmetry.md',
  'state-count-drift.md',
  'stub-mode-runtime-only-gate.md',
  'stub-mode-signature-no-proto-surface.md',
  'substitution-grammar-count-drift.md',
  'substitution-introspection-site-count.md',
  'timeout-policy-asymmetry.md',
].map(f => TN + f)

function chunk(arr, n) {
  const out = []
  for (let i = 0; i < arr.length; i += n) out.push(arr.slice(i, i + n))
  return out
}

const HARVEST_GUIDANCE = `
You are auditing the Rimsky orchestration platform for INTENT-vs-REALITY gaps.
Context: Rimsky is a reactive node-graph orchestration platform (Go). Despite
heavy planning, no client project has gotten it to work end-to-end as documented.
We suspect features that were planned/documented were never actually implemented,
were half-built, are buggy, or were explicitly deferred and never returned to.

GROUND TRUTH = the code. The docs you are handed are CLAIMS of intent only.
Your job: for each capability/behavior a handed doc claims Rimsky has (or should
have), OPEN THE ACTUAL CODE and determine whether it is real.

For EVERY claim you extract, you MUST attempt a real code check before reporting:
- Grep/read the relevant source under lib/, cmd/, test/ (NOT .ok-planner, NOT
  generated dirs lib/protocols/proto/v1/gen, NOT vendor/node_modules).
- A capability is only "implemented" if you can point to code that does it. A
  test passing against a stub is NOT proof the real path works.
- Distinguish: missing (no impl), partial (started, incomplete), buggy (impl
  exists but is wrong / contradicts the claim), deferred (doc explicitly says
  punted/TODO/follow-up and you confirmed it's still absent), implemented
  (verified real).

Be skeptical and concrete. Prefer file:line evidence. Mark confidence honestly:
"code-confirmed" only when you actually read the deciding code this run;
"likely" when strong indirect signal; "unsure" when you ran out of certainty.
Severity = consumer impact: blocker (a documented core workflow cannot work),
high, medium, low. blastRadius = what breaks for someone using Rimsky as
documented. Only report real gaps + a few notable confirmed-implemented items;
do NOT pad. If a handed doc is pure design musing with no implementable claim,
skip it.
`

const FINDINGS_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['findings'],
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['capability', 'source', 'sourceKind', 'claimedBehavior', 'status', 'codeEvidence', 'severity', 'blastRadius', 'confidence'],
        properties: {
          capability: { type: 'string', description: 'short name of the intended capability' },
          source: { type: 'string', description: 'doc path + section/line the claim came from' },
          sourceKind: { type: 'string', enum: ['divergence', 'tension', 'sketch', 'coverage', 'spec', 'invariant', 'doc-surface'] },
          claimedBehavior: { type: 'string', description: 'what the doc says should happen' },
          status: { type: 'string', enum: ['missing', 'partial', 'buggy', 'deferred', 'implemented'] },
          codeEvidence: { type: 'string', description: 'file:line proving status, or "none found despite checking X"' },
          severity: { type: 'string', enum: ['blocker', 'high', 'medium', 'low'] },
          blastRadius: { type: 'string' },
          confidence: { type: 'string', enum: ['code-confirmed', 'likely', 'unsure'] },
        },
      },
    },
  },
}

const VERDICT_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['capability', 'stillAGap', 'correctedStatus', 'evidence', 'severity'],
  properties: {
    capability: { type: 'string' },
    stillAGap: { type: 'boolean', description: 'true if after adversarial check this is still a real gap' },
    correctedStatus: { type: 'string', enum: ['missing', 'partial', 'buggy', 'deferred', 'implemented'] },
    evidence: { type: 'string', description: 'file:line you read; the implementation that refutes the gap, OR confirmation of absence' },
    severity: { type: 'string', enum: ['blocker', 'high', 'medium', 'low'] },
    notes: { type: 'string' },
  },
}

phase('Harvest')

const harvestJobs = []

chunk(divergences, 5).forEach((grp, i) => {
  harvestJobs.push({
    label: `harvest:divergences-${i + 1}`,
    prompt: `${HARVEST_GUIDANCE}

These are EXECUTION DIVERGENCE REPORTS — they record where plan execution
deviated, including work that was deferred, skipped, stubbed, or done
differently than planned. They are the single richest source of "we said we'd
do X and didn't." Read each one fully and extract every deferred / skipped /
incomplete / worked-around item, then code-check whether it was EVER completed
later (it may have been — check current code):

${grp.map(p => '- ' + p).join('\n')}

Return findings (use sourceKind "divergence").`,
  })
})

chunk(sketches, 6).forEach((grp, i) => {
  harvestJobs.push({
    label: `harvest:sketches-${i + 1}`,
    prompt: `${HARVEST_GUIDANCE}

These are DESIGN SKETCHES — proposed features/capabilities. Many may never have
become specs or code at all. For each sketch, extract the concrete capabilities
it proposes, then code-check whether each actually exists in the codebase today.
A sketch whose feature was never built is a pure intent-vs-reality gap (status
missing/deferred). Read each fully:

${grp.map(p => '- ' + p).join('\n')}

Return findings (use sourceKind "sketch").`,
  })
})

chunk(tensions, 13).forEach((grp, i) => {
  harvestJobs.push({
    label: `harvest:tensions-${i + 1}`,
    prompt: `${HARVEST_GUIDANCE}

These are OPEN DESIGN TENSIONS — explicitly unresolved questions, asymmetries,
or known-muddy behavior. Each describes a place where the system's behavior is
underspecified, inconsistent, or potentially wrong. Read each, then code-check
the ACTUAL current behavior: is it actually broken/inconsistent as the tension
fears, or was it since resolved? Report the ones where the code today exhibits a
real defect or a documented-but-unmet behavior:

${grp.map(p => '- ' + p).join('\n')}

Return findings (use sourceKind "tension").`,
  })
})

harvestJobs.push({
  label: 'harvest:coverage-and-remediation',
  prompt: `${HARVEST_GUIDANCE}

Read the prior acceptance-coverage diagnostic and the recent remediation
specs/plans, which already tried to identify and close intent-vs-reality gaps.
Your job: extract every gap they identified AND verify against current code
whether each was actually closed (the remediation may have claimed a fix that
didn't land, or only stubbed it). Also read the still-active dashboard spec/plan
(likely never executed):

- .ok-planner/coverage/2026-06-02-coverage-report.md
- .ok-planner/history/specs/2026-06-02-acceptance-coverage-recovery-design.md
- .ok-planner/history/plans/2026-06-02-acceptance-coverage-recovery.md
- .ok-planner/history/specs/2026-06-02-rimsky-core-remediation-design.md
- .ok-planner/history/specs/2026-06-03-instance-lifecycle-durable-by-default-design.md
- .ok-planner/specs/2026-05-02-dashboard-and-observability-design.md
- .ok-planner/plans/2026-05-02-dashboard-and-observability-plan.md

Return findings (use sourceKind "coverage" for the coverage report, "spec" for the specs/plans).`,
})

harvestJobs.push({
  label: 'harvest:load-bearing-invariants',
  prompt: `${HARVEST_GUIDANCE}

Rimsky describes load-bearing safety properties in concept docs by descriptive
name, and claims each is enforced at a code site and exercised by a scenario
test under test/scenarios/. Walk the concept-doc Invariants sections to
enumerate them. For EACH: (1) read the enforcing code and confirm it actually
enforces the stated property; (2) confirm a scenario test actually exercises
it (not just a stub). Report any invariant that is stated but NOT actually
enforced, or whose enforcement is buggy, or that has no real test. Also
grep '@concept:' to spot concept docs that claim behavior with no code behind
it if you encounter such.

Return findings (use sourceKind "invariant").`,
})

harvestJobs.push({
  label: 'harvest:documented-surface',
  prompt: `${HARVEST_GUIDANCE}

Check the DOCUMENTED USER-FACING SURFACE vs reality — "does Rimsky work as
documented." Look at:
- README.md (root) and examples/README.md and each examples/*/ program: do the
  documented usage flows actually work against the real code? Are the example
  programs real or stubs?
- CLI verbs under cmd/ (especially cmd/rimsky): is every documented subcommand
  actually wired to a working implementation, or are some stubbed / returning
  "not implemented"?
- HTTP routes (grep for chi router registrations, r.Post/r.Get/etc under
  lib/control): is every documented route actually handled, or are some
  registered-but-stubbed / missing?
- CLAUDE.md "Cross-cutting gotchas": each describes a behavior contract — spot
  check that the described behavior is actually implemented as stated.

Focus on the gap between what a new user would be told works and what actually
works end-to-end. Return findings (use sourceKind "doc-surface").`,
})

const harvested = await parallel(
  harvestJobs.map(j => () => agent(j.prompt, { label: j.label, phase: 'Harvest', schema: FINDINGS_SCHEMA }))
)

const allFindings = harvested.filter(Boolean).flatMap(r => r.findings || [])
log(`Harvested ${allFindings.length} raw findings from ${harvestJobs.length} sources`)

function sevRank(s) { return { blocker: 3, high: 2, medium: 1, low: 0 }[s] ?? 0 }
const byKey = new Map()
for (const f of allFindings) {
  const key = (f.capability || '').toLowerCase().replace(/[^a-z0-9]+/g, ' ').trim().slice(0, 80)
  if (!key) continue
  const prev = byKey.get(key)
  if (!prev || sevRank(f.severity) > sevRank(prev.severity)) {
    byKey.set(key, prev ? { ...f, source: prev.source + ' ; ' + f.source } : f)
  } else {
    prev.source = prev.source + ' ; ' + f.source
  }
}
const deduped = [...byKey.values()]
const gaps = deduped.filter(f => f.status !== 'implemented')
const passthroughImplemented = deduped.filter(f => f.status === 'implemented')
log(`${deduped.length} deduped; ${gaps.length} claimed gaps to adversarially verify`)

phase('Verify')

const verdicts = await parallel(gaps.map(f => () =>
  agent(`You are adversarially verifying a CLAIMED GAP in the Rimsky codebase.
Your DEFAULT is skepticism toward the gap: actively try to REFUTE it by finding
the real implementation that makes this capability work. Only if you genuinely
cannot find a working implementation do you confirm it as a real gap.

CLAIMED GAP:
- capability: ${f.capability}
- claimed status: ${f.status}
- what it should do: ${f.claimedBehavior}
- harvester's evidence: ${f.codeEvidence}
- source doc(s): ${f.source}

Do this:
1. Search the actual code (lib/, cmd/, test/; skip .ok-planner, generated gen/,
   vendor, node_modules). Try hard to find an implementation that satisfies the
   claimed behavior end-to-end.
2. If you find it works: stillAGap=false, correctedStatus=implemented, cite the
   file:line that proves it.
3. If it's there but incomplete/wrong: stillAGap=true, correctedStatus=
   partial|buggy, cite the deficient code.
4. If truly absent: stillAGap=true, correctedStatus=missing|deferred, cite what
   you searched ("grepped X, Y, Z under lib/...; no handler").
Set severity to the consumer impact you now believe is accurate. Be concrete and
cite file:line you actually read this run.`,
    { label: `verify:${(f.capability || 'gap').slice(0, 40)}`, phase: 'Verify', schema: VERDICT_SCHEMA }
  ).then(v => v ? { ...f, verdict: v } : null)
))

const verified = verdicts.filter(Boolean)
const confirmedGaps = verified.filter(x => x.verdict.stillAGap)
const refuted = verified.filter(x => !x.verdict.stillAGap)
log(`Verified ${verified.length}: ${confirmedGaps.length} confirmed gaps, ${refuted.length} refuted (actually implemented)`)

phase('Synthesize')

const SYNTH_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['reportMarkdown', 'topGaps', 'blockerCount', 'highCount', 'mediumCount', 'lowCount'],
  properties: {
    reportMarkdown: { type: 'string', description: 'the full report markdown, ready to write to a file' },
    topGaps: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['capability', 'severity', 'oneLine'],
        properties: {
          capability: { type: 'string' },
          severity: { type: 'string', enum: ['blocker', 'high', 'medium', 'low'] },
          oneLine: { type: 'string' },
        },
      },
    },
    blockerCount: { type: 'number' },
    highCount: { type: 'number' },
    mediumCount: { type: 'number' },
    lowCount: { type: 'number' },
  },
}

const confirmedForSynth = confirmedGaps.map(x => ({
  capability: x.capability,
  status: x.verdict.correctedStatus,
  severity: x.verdict.severity,
  claimedBehavior: x.claimedBehavior,
  blastRadius: x.blastRadius,
  evidence: x.verdict.evidence,
  source: x.source,
  sourceKind: x.sourceKind,
  notes: x.verdict.notes || '',
}))
const refutedForSynth = refuted.map(x => ({
  capability: x.capability,
  whyClaimed: x.claimedBehavior,
  actuallyImplemented: x.verdict.evidence,
  source: x.source,
}))

const synth = await agent(
  `You are writing the FINAL intent-vs-reality gap audit report for the Rimsky
orchestration platform. The user's complaint: no client project has ever gotten
Rimsky to work end-to-end as documented, and they suspect many planned/documented
features were never really built. You have the adversarially-CONFIRMED gaps and
the REFUTED claims (things a harvester thought were missing but are actually
implemented) below as JSON.

Write a thorough, honest markdown report. Structure:
1. Title + one-paragraph executive summary that directly answers: where is the
   biggest intent-vs-reality drift, and is the user's suspicion borne out?
2. A "Blockers" section: gaps that prevent a documented core workflow from
   working at all — these are the most likely reasons a client can't get Rimsky
   to work. Each as a tight entry: capability, what's claimed, what's real
   (with file:line evidence), blast radius, source.
3. "High", "Medium", "Low" severity sections, same entry shape, kept tight
   (table or short entries — do not bloat).
4. A "Refuted / actually fine" appendix: claims that looked like gaps but the
   code does satisfy — so the reader knows these were checked and cleared.
5. A short "Themes" closing: the 3-5 systemic patterns behind the gaps (e.g.
   "documented but stubbed", "happy-path only", "wired in config but no
   handler"), since the user wants to stop tripping over this class of problem.

Be precise; every gap entry must carry the file:line evidence from the data.
Group/merge obvious duplicates. Order each section by blast radius. Do NOT
invent gaps not in the data. Cite sources as the doc paths given.

Also return topGaps (the most important confirmed gaps, capped ~12) and the
counts by severity.

CONFIRMED GAPS (JSON):
${JSON.stringify(confirmedForSynth, null, 1)}

REFUTED CLAIMS (JSON):
${JSON.stringify(refutedForSynth, null, 1)}`,
  { label: 'synthesize-report', phase: 'Synthesize', schema: SYNTH_SCHEMA }
)

return {
  rawFindingCount: allFindings.length,
  dedupedCount: deduped.length,
  verifiedCount: verified.length,
  confirmedGapCount: confirmedGaps.length,
  refutedCount: refuted.length,
  passthroughImplementedCount: passthroughImplemented.length,
  blockerCount: synth?.blockerCount ?? 0,
  highCount: synth?.highCount ?? 0,
  mediumCount: synth?.mediumCount ?? 0,
  lowCount: synth?.lowCount ?? 0,
  topGaps: synth?.topGaps ?? [],
  reportMarkdown: synth?.reportMarkdown ?? '',
}
