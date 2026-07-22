export const meta = {
  name: 'gap-audit-revision-completed-plans-only',
  description: 'Re-anchor each non-sketch gap to the COMPLETED plan/spec/guarantee that claimed it done; drop forward-facing roadmap',
  phases: [
    { title: 'Re-anchor', detail: 'tie each candidate to a completed plan/spec or drop as roadmap' },
    { title: 'Synthesize', detail: 'rewrite report: completed-but-not-delivered only' },
  ],
}

// COMPLETED plans live in history/plans, completed specs in history/specs.
// The only ACTIVE (un-executed) plan/spec is the dashboard one. Sketches in
// .ok-planner/sketches/ are forward-facing roadmap and are OUT OF SCOPE unless
// the same capability was ALSO promised by a completed plan/spec/guarantee.

const GUIDANCE = `
We are revising a gap audit of the Rimsky orchestration platform under a SHARPER,
NARROWER question than before:

  >> Which COMPLETED plans/specs (or shipped documented guarantees) claimed a
     deliverable as DONE, but the actual code does NOT deliver it?

Out of scope now (DROP): forward-facing roadmap — anything whose only home was a
design SKETCH under .ok-planner/sketches/ that never became an executed plan.
"We never built the package manager" is NOT interesting. "A completed plan said
X works and it doesn't" IS exactly what we want.

Where completed work lives:
  - Completed plans:  .ok-planner/history/plans/*.md  (and *-divergences.md =
    that plan's own honest record of what it punted)
  - Completed specs:  .ok-planner/history/specs/*.md
  - Shipped guarantees: CLAUDE.md "MUST" statements & cross-cutting gotchas;
    invariant annotations; concept docs under .ok-planner/design/concepts/
  - A few early completed plans/specs are under .ok-planner/archive/

Code is ground truth (lib/, cmd/, test/; ignore generated gen/, vendor,
node_modules). For the ONE candidate handed to you:

1. Find the COMPLETED plan/spec/guarantee that presented this capability as
   delivered/working. Read it. Quote the line that claims it (or its acceptance
   criterion / divergence note).
2. Decide the anchor:
   - completed-plan / completed-spec / shipped-invariant / shipped-doc-guarantee
     = a completed artifact claimed this done.
   - deferred-never-resumed = a completed plan's divergence report EXPLICITLY
     recorded this as punted "to a follow-up" and the follow-up never happened
     (still a real intent-vs-reality gap: the plan shipped knowing it was a hole).
   - drop-roadmap = only ever a sketch / never claimed done by any completed
     artifact. (Set stillAGap=false.)
   - drop-intentional = a completed plan/spec DELIBERATELY and explicitly scoped
     this out and DOCUMENTED the absence (not a broken promise). (stillAGap=false.)
3. Re-verify the code reality with file:line you actually read this run.
4. Classify category and severity by consumer blast radius.

Be rigorous and skeptical. If you cannot find a completed artifact that claimed
it done, it is drop-roadmap — do not keep it on a hunch.
`

const candidates = [
  { cap: 'rimsky run <file> ships no copyable example TemplateSpec', hint: 'The `rimsky run <file>` verb + CLI shipped via a completed CLI plan (.ok-planner/archive/2026-05-02-rimsky-cli-and-compose-plan.md and later). The root README.md (exists, 18KB) presents the dev loop. Confirm a completed plan/README presents `rimsky run <file>` as a working onboarding step, then confirm no copyable spec YAML ships.', ev: 'cmd/rimsky/cli/run.go works; README.md has zero fenced code; examples/ are gRPC Go programs; only node.TemplateSpec shapes are inline Go test structs' },
  { cap: 'Bundled postgres store does not implement claim-producer SplitScope', hint: 'Check the COMPLETED fan-out plan .ok-planner/history/plans/2026-05-22-fan-out-safety-scope-first.md and services-reintegration (2026-05-27) and concept:fan-out. Did a COMPLETED plan/concept claim fan_out: nodes work end-to-end against the bundled stores? Or was bundled-store split-scope ONLY the 2026-05-28 sketch (never executed)? If only the sketch and the completed fan-out plan never promised bundled-store support, this is drop-roadmap.', ev: 'lib/services/stores/postgres/server/server.go:128 UnimplementedClaimProducerServer, no SplitScope, SupportsSplitScope unset; lib/graph/node/template_validator_holds.go:165-170 rejects fan_out against it' },
  { cap: 'Bundled filesystem store does not implement claim-producer SplitScope', hint: 'Same anchoring question as the postgres SplitScope item — completed fan-out plan vs the 2026-05-28 sketch. lib/services/stores/filesystem/server/server.go:108-122.', ev: 'filesystem store server embeds UnimplementedClaimProducerServer, no SplitScope' },
  { cap: 'Idempotency-Key documented MUST but not enforced (no 400 on missing header)', hint: 'CLAUDE.md cross-cutting gotcha says "Every publisher message-emit MUST carry the Idempotency-Key HTTP header." This shipped via completed plan .ok-planner/history/plans/2026-05-17-sensor-messaging-unification.md. Confirm the MUST claim, then confirm code does not 400.', ev: 'lib/control/controlapi/messages.go:167 reads header, :217 gates dedup on non-empty, :301 returns 201; no StatusBadRequest for missing header; messages_test.go:274 asserts 201 with no header set' },
  { cap: 'Anonymous mode and late-bound services are mutually exclusive', hint: 'Anonymous mode shipped via completed .ok-planner/history/plans/2026-05-15-control-plane-mcp-and-auth.md; late-bind via completed .ok-planner/history/plans/2026-05-24-host-agent-and-proxy.md. Did EITHER completed plan claim the two compose (anonymous instances can use late-bound services)? If neither completed plan promised the interaction, this is an unhandled interaction surfaced only in an open tension — lean drop-roadmap unless a completed plan claimed it works.', ev: 'AnonymousIdentity KeyID=nil -> null instance owner; proxy dispatch.go:117-119 returns host_agent_not_connected for anonymous-owned instances' },
  { cap: 'SQLite + replicas>1 has no symmetric fail-fast startup gate', hint: 'The unified-image + pluggable persistence shipped via completed .ok-planner/archive/2026-05-02-persistence-pluggable-and-unified-image-plan.md (and later). Did a completed plan claim a symmetric fail-fast gate for sqlite-under-replicas, or did it only ever document sqlite as single-node? If the latter, the code matches the completed plan (drop-roadmap / not-a-broken-deliverable). The `memory` backend IS gated.', ev: 'blob_config.go:115-117 gates memory; sqlite/database.go:139-142 only slog.Warn; no role/replica check in open.go' },
  { cap: 'Callback advertise-host misconfig fails silently for routable typos', hint: 'Supervisor callback shipped via completed .ok-planner/history/plans/2026-05-24-host-agent-and-proxy.md. Did the completed plan claim fail-fast validation / self-probe of advertise_host, or only a warn? If only a warn was speced, code matches plan.', ev: 'cmd/rimsky-supervisor/main.go:174-180 warns only for empty/loopback; routable-but-wrong host only log.Info, dispatches orphan-reap silently' },
  { cap: 'compose: prefix reservation enforced client-side only', hint: 'rimsky compose shipped via a completed plan (.ok-planner/archive/2026-05-02-rimsky-cli-and-compose-plan.md or a history plan). Did a completed plan/concept claim the compose: tag/instance-key reservation is enforced (server-side)? If it was only ever a CLI-side convention, server has no obligation.', ev: 'cmd/rimsky/cli/templates.go:209,236-237 client-side guard; server validTag tags.go:30-38 allows ":" with no compose guard; instance-key path no prefix check' },
  { cap: 'No-internal-serialization-for-staged-async guarantee has no enforcement site and no conformance probe', hint: 'CLAUDE.md states the project guarantee: every load-bearing invariant has an enforcing code site AND a scenario test. Confirm the no-internal-serialization rule (ClaimProducers MUST NOT internally serialize on lock-shaped predicates) has neither.', ev: 'rule lives only as interface comments claimproducer.go:23-26, locks/interface.go:54-57; no enforcement site; no conformance probe; scenario test exercises only rimsky-side consequence' },
  { cap: 'Unified 5x-heartbeat orphan cutoff is two intervals, not one', hint: 'Orphan-reaper/heartbeat shipped via a completed plan (.ok-planner/history/plans/2026-05-05-reactive-loops-and-lifecycle-handlers.md and/or 2026-06-03-instance-lifecycle-durable-by-default.md). Does the orphan-cutoff rule / a completed plan claim a SINGLE unified cutoff? Code has two base intervals.', ev: 'two representations + two base intervals (15s vs 5s -> 75s vs 25s); 5x hardcoded at 6 sites, no shared constant; cutoff prose claims single cutoff' },
  { cap: 'rimsky_events.kind has no schema enforcement', hint: 'Event-log shipped via completed plans (concept:event-log). Did any completed plan/spec/concept claim event-kind values are VALIDATED/enumerated? If the design always intended free-form TEXT, this is by-design (drop). ValidateTypePath exists but is dead test-only code.', ev: 'rimsky_events.kind free-form TEXT, no CHECK/FK/write-validation; ValidateTypePath unused in production' },
  { cap: 'Quality-rule typed Severity enum is unwired (zero consumers)', hint: 'Quality rules shipped via completed .ok-planner/history/plans/2026-05-28-quality-of-life-features.md. Did the completed plan claim a wired warning/error severity partition? Code has the enum type but no consumer and dropped the partition.', ev: 'typed Severity enum exists with zero consumers; verifier-shape-checks treats every fail as blocking; old ==\"warning\" footgun removed' },
  { cap: 'stub-mode conformance signature has no single source of truth', hint: 'Stub-mode handshake shipped via completed conformance/collapse-sdk plans (2026-05-26). Did a completed plan claim a single shared constant? If this is only an open tension about fragility (handshake works today), lean drop-roadmap.', ev: '"stub_probe"/{stub:true} hardcoded at ~15 Go sites + TS independently; works today but rename-fragile' },
  { cap: 'Coalesced-fire produces no insert-vs-coalesce audit signal', hint: 'Frame coalescing shipped via completed signal-taxonomy plan (2026-05-23). Did a completed plan claim coalesce observability? If only an open tension, lean drop. The cited schedule_fired symptom may be stale (kind retired).', ev: 'frame producer emits no insert-vs-coalesce signal; reconstructable from source_node_ids[]' },
  { cap: 'watch live-tail is source-grouped, not chronological', hint: 'watch shipped via completed instance-debugger plan (2026-05-24). Did the completed plan/doc-comment claim a chronological interleaved feed? code groups per poll cycle.', ev: 'cmd/rimsky/cli/watch.go:62-142 groups events->hits->terminal per cycle; doc-comment :8-9 says chronological; rows carry true timestamps' },
  { cap: 'rules.md references non-existent deploy/build-images.sh and deploy/docker-compose.yml', hint: 'A completed reorg plan (.ok-planner/history/plans/2026-05-24-repo-reorganization.md or 2026-05-27-root-folder-reorg.md) moved/removed deploy/. .claude/rules/rules.md:20 still instructs using deploy/ artifacts that no longer exist. doc-drift in a live project rule.', ev: 'no deploy/ dir; rules.md "Reference-binary or deploy changes" step cites deploy/build-images.sh + deploy/docker-compose.yml; real path is make core-images + testcontainers' },
  { cap: 'wait-set topic_kind enum collapses the 5-value signal taxonomy to 3', hint: 'Completed signal-taxonomy plan (2026-05-23) divergence recorded the CHECK-broadening migration as deferred. deferred-never-resumed.', ev: 'runtime adapter waitSetTopicKindFor collapses taxonomy into legacy 3-value enum; audit-log retains full path; CHECK-broadening migration deferred' },
  { cap: 'sensor-cron has no persistent state DB', hint: 'A completed sensor plan recorded this as deliberately deferred (next_fire_at reconstructible). Decide: deferred-never-resumed vs drop-intentional (if the completed plan said deliberate-and-acceptable).', ev: 'no state_db.go, no RIMSKY_SENSOR_CRON_STATE_DSN; runtime resync restores subs; <=1 missed fire per restart' },
  { cap: 'MCP-auth conformance probe (--mode=auth-mcp) never built', hint: 'Completed .ok-planner/history/plans/2026-05-15-control-plane-mcp-and-auth.md divergence recorded the conformance-binary mode deferred; behavior covered by in-process unit + scenario tests. deferred-never-resumed (the runnable packaging is the gap).', ev: 'no conformance-binary auth-mcp mode; M2 assertions exist as in-process MCP unit tests + by-grant scenario test' },
  { cap: 'Embedded-source + literal-fallback recovery e2e scenario missing', hint: 'Completed .ok-planner/history/plans/2026-05-20-multi-source-substitution-decline.md divergence. Embedded-source + fallback ARE covered e2e; only the lenient-?-marker producer-recovery e2e is missing. test-gap from a completed plan.', ev: 'embedded_source_test.go + fallback_test.go cover e2e; z_pattern_producer_recovery lenient-? e2e missing; ? marker unit-tested only' },
  { cap: 'Negative max_signoff_attempts: parser keeps it, gate coerces to 3', hint: 'Completed .ok-planner/history/plans/2026-06-04-claude-agent-signoff-gate.md. Decide if this is a real completed-plan gap or cosmetic-unreachable (JSON-schema minimum:0 rejects at registration).', ev: 'parser keeps negatives; gate coerces to 3; unreachable due to schema minimum:0' },
  { cap: 'await_async-stuck terminate + backfill-override tested at unit altitude, not full-stack scenario', hint: 'Completed plan(s) (instance-lifecycle-durable 2026-06-03 and/or quality-of-life 2026-05-28) divergence: the spec strategy demanded full-stack scenario coverage; only handler/runtime-unit tests with fakes exist. test-altitude gap from a completed plan.', ev: 'both behaviors tested with fakes at handler/runtime unit level; no full-stack scenario as the completed spec demanded' },
  { cap: 'Dashboard reference SPA not in repo', hint: 'The dashboard spec is the only ACTIVE (un-executed) spec. The SPA was deliberately carved to a sibling repo (commit c1ce756) and the absence is documented in feature-index.md. This is almost certainly drop-intentional (documented carve-out), NOT a completed-plan-not-delivered. Confirm.', ev: 'no in-repo SPA; feature-index.md documents the absence; backend observability surfaces remain in-repo' },
]

phase('Re-anchor')

const RE_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['capability', 'anchorKind', 'anchorPath', 'planClaimedDone', 'claimText', 'codeReality', 'stillAGap', 'category', 'severity', 'reframed'],
  properties: {
    capability: { type: 'string' },
    anchorKind: { type: 'string', enum: ['completed-plan', 'completed-spec', 'shipped-invariant', 'shipped-doc-guarantee', 'none-roadmap-only', 'intentional-documented-deferral'] },
    anchorPath: { type: 'string', description: 'path to the completed plan/spec/doc that claimed it, or "none"' },
    planClaimedDone: { type: 'boolean', description: 'did a completed artifact present this as delivered/working?' },
    claimText: { type: 'string', description: 'quoted/paraphrased line from the completed artifact claiming it (or its deferral note)' },
    codeReality: { type: 'string', description: 'file:line you read this run' },
    stillAGap: { type: 'boolean', description: 'false for drop-roadmap and drop-intentional' },
    category: { type: 'string', enum: ['completed-not-delivered', 'completed-partial', 'completed-buggy', 'deferred-never-resumed', 'doc-drift', 'unenforced-guarantee', 'drop-roadmap', 'drop-intentional'] },
    severity: { type: 'string', enum: ['blocker', 'high', 'medium', 'low'] },
    reframed: { type: 'string', description: 'a tight report entry: what the completed artifact claimed vs what the code does, with evidence' },
  },
}

const reAnchored = await parallel(candidates.map(c => () =>
  agent(`${GUIDANCE}

CANDIDATE TO RE-ANCHOR:
- capability: ${c.cap}
- prior code evidence: ${c.ev}
- where to look for the completed-artifact anchor: ${c.hint}

Return the re-anchored verdict.`,
    { label: `re-anchor:${c.cap.slice(0, 38)}`, phase: 'Re-anchor', schema: RE_SCHEMA }
  )
))

const verdicts = reAnchored.filter(Boolean)
const kept = verdicts.filter(v => v.stillAGap)
const droppedRoadmap = verdicts.filter(v => v.category === 'drop-roadmap')
const droppedIntentional = verdicts.filter(v => v.category === 'drop-intentional')
log(`Re-anchored ${verdicts.length}: kept ${kept.length}, dropped-roadmap ${droppedRoadmap.length}, dropped-intentional ${droppedIntentional.length}`)

// Forward-facing sketch items removed wholesale (named so the user sees them go)
const removedSketches = [
  'Package manager CLI + catalog',
  'geo blessed typed-attribute (PostGIS/GeoParquet/CRS)',
  'Rimsky Development Kit (Python rimsky-rdk + python-runtime executor)',
  'Distributed tracing (OpenTelemetry spans / traceparent / trace columns)',
  'SSE event stream GET /events/stream',
  'Template-level message schema (messages: block)',
  'Durable audit-log spool + background shipper',
  'Dynamic executor / claim-producer registration endpoints (POST)',
  'Breakpoint-hit push delivery + breakpoint.hit event kind',
  'Unified child-execution primitive (DispatchChildExecution/SettleChildExecution) + carry_verbatim',
  'Executor subprocess lifecycle events (executor.subprocess_*, cost_recorded)',
  'Heartbeat enrichment columns (last_subprocess_stdout_at, last_callback_kind)',
  'One-message-per-frame invariant',
  'Multi-tenant substore provisioning verbs',
  'report_await_async_callback MCP tool',
  '_rimsky.run_scope.* reserved attributes to fan-out children',
  'Reactive-nomenclature rework (event/emit/subscribe rename)',
  'Cheap-test stub compose profile + synthetic-blocker binary',
  'Single shared delegation/fan-out runtime primitive (internal refactor)',
]

phase('Synthesize')

const SYNTH_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['reportMarkdown', 'topGaps', 'counts'],
  properties: {
    reportMarkdown: { type: 'string' },
    topGaps: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['capability', 'severity', 'category', 'oneLine'],
        properties: {
          capability: { type: 'string' },
          severity: { type: 'string' },
          category: { type: 'string' },
          oneLine: { type: 'string' },
        },
      },
    },
    counts: {
      type: 'object',
      additionalProperties: false,
      required: ['notDelivered', 'partial', 'buggy', 'deferredNeverResumed', 'docDrift', 'unenforcedGuarantee'],
      properties: {
        notDelivered: { type: 'number' },
        partial: { type: 'number' },
        buggy: { type: 'number' },
        deferredNeverResumed: { type: 'number' },
        docDrift: { type: 'number' },
        unenforcedGuarantee: { type: 'number' },
      },
    },
  },
}

const synth = await agent(
  `You are rewriting the Rimsky gap audit under a NARROWED scope: ONLY gaps where
a COMPLETED plan/spec (or a shipped documented guarantee / load-bearing invariant)
claimed something done, but the code does not deliver it. ALL forward-facing
sketch/roadmap items have been removed.

The user's exact concern: "plans that were allegedly completed, but really weren't."
The user is frustrated that no client has gotten Rimsky working end-to-end despite
heavy planning. Be direct and honest.

Write a thorough markdown report:
1. Title + executive summary answering: of the work that was supposedly FINISHED,
   how much actually shipped working? Lead with the most damning pattern.
2. "Completed but not delivered / partial / buggy" — the core. Group by category.
   Each entry: capability, what the COMPLETED plan/spec/guarantee claimed (cite
   the anchor path + claim), what the code actually does (file:line), blast
   radius. Order by blast radius. Use tight tables where possible.
3. "Recorded as deferred in a completed plan, never resumed" — the honest punts
   that were supposed to be follow-ups and weren't.
4. "Unenforced documented guarantees" — CLAUDE.md MUSTs / load-bearing invariants the
   code doesn't actually hold (most corrosive: operators rely on them).
5. "Doc-drift inside completed work" — stale instructions/comments shipped by
   completed plans.
6. "Removed from this revision (forward-facing roadmap)" — a tight bullet list
   naming the dropped sketch items, so the reader sees they were deliberately
   excluded as never-promised-by-a-completed-plan.
7. "Deliberately deferred & documented (not gaps)" — intentional carve-outs.
8. "Themes" — the 3-5 systemic patterns behind completed-but-not-really-done.

Every kept entry MUST carry both the completed-artifact claim citation AND the
file:line code evidence. Do not invent. Do not re-add roadmap items into the gap
sections.

KEPT GAPS (JSON):
${JSON.stringify(kept, null, 1)}

DROPPED AS ROADMAP (JSON, for the removed-items section):
${JSON.stringify(droppedRoadmap.map(v => ({ capability: v.capability, why: v.claimText, anchor: v.anchorPath })), null, 1)}

DROPPED AS INTENTIONAL/DOCUMENTED (JSON):
${JSON.stringify(droppedIntentional.map(v => ({ capability: v.capability, why: v.claimText, anchor: v.anchorPath })), null, 1)}

ALSO REMOVED — pure forward-facing sketches never sent for re-anchoring (list verbatim in the removed-items section):
${JSON.stringify(removedSketches, null, 1)}

Return reportMarkdown, topGaps (the most important kept gaps, ~10), and counts.`,
  { label: 'synthesize-revised-report', phase: 'Synthesize', schema: SYNTH_SCHEMA }
)

return {
  reAnchoredCount: verdicts.length,
  keptCount: kept.length,
  droppedRoadmapCount: droppedRoadmap.length,
  droppedIntentionalCount: droppedIntentional.length,
  removedSketchCount: removedSketches.length,
  counts: synth?.counts ?? {},
  topGaps: synth?.topGaps ?? [],
  keptVerdicts: kept.map(v => ({ capability: v.capability, category: v.category, severity: v.severity, anchorKind: v.anchorKind, anchorPath: v.anchorPath, planClaimedDone: v.planClaimedDone })),
  reportMarkdown: synth?.reportMarkdown ?? '',
}
