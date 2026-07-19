# Phase 2 progress — proofs and tests

Live tracking file for the Phase 2 fleet run (see
`2026-07-13-drift-remediation-execution-plan.md`, Phase 2 section). Updated by
the orchestrator after every batch. If a session dies, resume from this file:
each cluster row says whether its work is unstarted / in-flight / verified /
staged. Cluster row lists live in the session scratchpad at
`phase2-clusters/*.json`; if the scratchpad is gone, regenerate them from the
ledger (`review-findings-2026-07-06.csv`) — clusters are keyed by ledger ids
listed below.

## Discipline

- Agents leave changes UNSTAGED; orchestrator verifies each batch (build +
  affected tests), then `git add -A` as a checkpoint. Commits only on user
  request.
- Models: Sonnet for test-writing clusters and the determinism sweep; Opus for
  the feature-loss dossier sweep (silent-failure-mode judgment).
- All new/changed tests must be deterministic per rules.md (no wall-clock
  verdicts, no sleeps-to-settle).
- Determinism cluster runs BEFORE the scenarios/stories clusters (it rewrites
  the shared wait helpers they build on).

## Pass 1+3 clusters (test-writing, Sonnet)

| cluster | rows (ledger ids) | batch | status |
| --- | --- | --- | --- |
| controlapi-a | 782 779 826 836 815 816 900 899 | 1 | verified+staged (bugs fixed: lineage ancestors walked non-run substitution refs; MCP admin-route deprioritization dead-check; MCP tool→route misrouting for auth_status/asset sub-resources — explicit Tool pin added) |
| controlapi-b | 94 743 764 765 737 787 788 790 774 338 820 821 | 1 | verified+staged (bugs fixed: message replay-after-termination 409ed instead of 200; run-scope fanout failure silently dropped from terminator retry set; held-claim cleanup on force-terminate no-op'd via 'instance-killed' sentinel passed as claimant guard) |
| locks | 983 973 965 969 970 971 972 | 1 | verified+staged (4 fake bugs fixed: realized-semantics default, ScopesConflict gate/byte-equal, dropped spec fields, dropped lifecycle fields; Fake now runs under claim-producer conformance) |
| determinism | 2364 2365 + helper sweep | 1 | verified+staged (30+ helpers deadline-free, ~320 call sites, 129 files; 2365 root-caused: WithWaitStrategy replaced the module's port-wait — fixed with ForAll(log,port) + boot-helper consolidation; lint ban assessed infeasible without a custom go/analysis pass; FOLLOW-UP: ~60 files of inline deadline loops + 42 require.Eventually sites recommended as a new ledger row) |
| conformance | 1399 117 365 1431 120 1426 1436 | 2 | verified+staged (strengthened checks exposed real defects: ALL FOUR bundled sensors never returned resolved_config on ListSubscriptions — fixed + round-trip tests; http-node park probe ignored reason_label and hard-coded resume_at — fixed; stub lacked the stub_probe handshake so RequiresStub scenarios silently never ran — fixed; cancel scenario rebuilt as stub-mode two-callback proof; terminal idempotency now covers abandon+release; unknown_ack_id scenario deleted as misfiled. FLAGGED for later: claude-agent executor has no reason_label plumbing) |
| launch-config-obs | 915 916 914 938 346 707 2248 | 2 | verified+staged (bug fixed: license-check verifyEntriesExist never checked exempt entries — fix surfaced 2 stale licensing.yml entries, removed; supervisor/scheduler config resolution extracted for testability; orchestrator drive-by: added missing license headers to migrations 019-023 both drivers, license-check now 0 violations) |
| persistence-events | 1008 1012 1144 1190 960 | 2 | verified+staged (all 5 rows test-only; blob ReadRange contract pinned per-backend, event cursor pagination + 4 wait-set gate methods added to the storage conformance suite on both drivers; no code bugs) |
| runtime-graph | 373 127 133 139 140 382 141 1372 1373 1483 | 2 | verified+staged (bug fixed: routeHeldClaimVerbError never emitted deferred held-cascade, leaving co-holders stuck in held forever on the producer-verb-error fallback path; claim-tree invariants 1/3/5 + multi-supervisor scope filter + malformed-aggregation fallback now proven; 141 already fixed at HEAD; vacuous validator/scheduler tests replaced and adversarially verified) |
| scenarios-a | 2164 2165 2170 2171 2172 2169 144 386 387 2185 2186 2195 2192 2193 | 3 | verified+staged (production bug fixed: 4 call sites silently zeroed LineageHint.InstanceID on failed acquirer lookup — new acquirerInstanceID helper errors instead; asset routes confirmed correctly gated with new authorization matrix test; rows 144/386/387 already closed at tip; misnamed fanout projection test renamed) |
| scenarios-b | 2211 2208 2139 2142 2141 2223 2144 2220 2221 2227 2149 2147 2222 2155 | 3 | verified+staged (2 production bugs fixed: work_completed emitted per in-place retry iteration — now one pair per work_started; sqlite PruneTraceForRetention ran the window query twice in one write tx starving writers — snapshot-then-delete fix. Sensor tests now drive the REAL publisher-subscription lifecycle incl. new HarnessOpts.Publishers; first e2e proof of walkUpwards/Aggregate persisted tree; row 2149 subgraph defects did NOT reproduce at tip — permanent regression test added, ResolvesViaCallingNode noted as dead flag) |
| stories-claude-agent | 2306 2307 2308 2309 2310 1996 | 3 | verified+staged (fresh-session-in-sub-graph proven via caller-bag assertions; module/http-loopback transport now scenario-covered — required a fakeclaudeserver binary + extracting executor serving stack into serve.go, pure refactor; rejection subtests now assert agent/attribute_invalid + named entry/instance/node payload; code already correct, no bugs; images rebuilt) |
| stories-cascade-held | 2304 2319 2322 2326 2311 | 3 | verified+staged (all 5 proof-only: tag-absent non-fire leg, held-moment gating barriers via HoldUntil stub gate, wait-set/lineage tie, same-frame back-edge assertion, overreach-vs-cascade discrimination via creation_reason; no code bugs) |
| stories-misc | 2296 2300 2301 2325 146 2330 2333 2345 | 4 | verified+staged (production bug fixed: release_and_requeue reused the node_run row, so evaluateHolderPortfolio poisoned the successful re-dispatch via the historically-abandoned handle — now deletes non-held abandoned handles for the run after release; carry-forward + idempotent-settled + coalesce-which-survives proofs written; portable-template extended to a claim producer via new WithBundledFilesystemClaimProducer harness cap; CreateInstanceWithServiceBindings now waits for root dispatch) |
| stories-obs-auth | 2299 2327 2316 2318 2329 2343 2344 | 4 | verified+staged (agent stranded by session-limit crash mid-e2e; orchestrator completed verification. 6 of 7 rows landed as green proofs [anonymous-mode leak-guard prefix fixed; per-run-scope reap-isolation falsifier; trace history-fidelity; event-log since/until window; named-lock real-acquire counter; substitution doc-accuracy gate]. lifecycle.proto doc-comment corrected re host-agent-proxy reap keying by run_scope_id. Row 2344 [wrap sub-claim-payload demo.sh in a Go e2e]: e2e written but the feature it exercises — filesystem list-split fan-out per-sub-claim payload substitution — does NOT complete end-to-end: child runs cannot acquire synthesized parent/_list/<key> sub-claims [settle acquire/unavailable; hang under retry]. Unproven at every layer. Test t.Skip'd citing new restore-feature ledger row 2366; feature gap queued for adjudication. Stray fanout_seed→fanout/seed rename kept [current validation REQUIRES slash-bearing message types]; error_types acquire/unavailable:retry added to satisfy validator. FOLLOW-UP: sibling fanout-fs-list-array template still uses stale underscore type — would 400 on deploy) |

Image rebuild (core+service+test) running in background alongside batch 3. Before batch 3: rebuild images (`make core-images service-images test-images`)
so the services harness proves current source.

## Pass 2 — feature-loss dossier sweep (read-only, Opus)

75 dossiers under `.ok-planner/design/intent/` (excluding `_retired.md`,
`area--misc.md`), batched ~8 dossiers per agent, interleaved with batches 2+.
Output: per-behavior verdicts (guarded / unguarded / missing-in-code);
orchestrator appends `restore-feature` rows to the ledger mechanically.
Guard-test misses feed a follow-on writing wave after batch 4.

74 dossiers cut into 8 waves (lists in scratchpad `phase2-clusters/sweep-{0..7}.json`);
launched as writer batches free up capacity.

| sweep wave | dossiers | status |
| --- | --- | --- |
| sweep-0 | blob-backend claim-handle data-processing frame lineage node publisher signal validation | DONE: 168 behaviors — 125 guarded, 33 write-test, 10 restore-feature (results in scratchpad phase2-sweep-results/sweep-0.json; headline items: 2026-07-14 frame-timeout + park-watchdog + transient-subscribability + retired-config-directing-error erasures all UNEXECUTED with green tests guarding old behavior; frame terminated-state + kill-all-five-states missing per 07-17 ruling; lifecycle-subscriber validation role missing; lineage claim routes mislabeled; lineage frame-trigger fields dead; node settling_signal_type crept back vs derived-state sweep — needs adjudication) |
| sweep-1 | advisory-lock breakpoint claim-lifetime delegation graph message-schema observability replica sub-graph wait-set | DONE: 137 behaviors — 105 guarded, 19 write-test, 13 restore-feature (headliners: pause-mode breakpoints fail OPEN on infra error CONTRADICTING the 07-17 fail-closed ruling [tracker previously misstated this as conforming — corrected during adjudication]; message bodies NOT schema-validated at insert with an INVERTED test locking the defect in; fan-out+delegation composition validator-forbidden though promised; declared_tags registration gate downgraded reject→warn; several artifact-tier items flagged likely-superseded needing dossier reconciliation not restoration) |
| sweep-2 | anonymous-mode cancel-siblings claim-producer discovery-cache host-agent-proxy message-sender-node orphan-reaper rimsky-yml supervisor write-semantics | DONE: 150 behaviors — 94 guarded, 37 write-test, 19 restore-feature (results in scratchpad phase2-sweep-results/sweep-2.json; headliners: 07-14 producer-verb OUTBOX ruling never built; host-agent-proxy trim unexecuted with anti-guard test; ref_validation_mode retirement unexecuted; 'first' policy cancel actions computed but discarded by every caller; callback-advertise fail-fast never landed; recovery-disposition stamping absent) |
| sweep-3 | api-key cascade-graph claim-scope dry-run host-agent message parked-state rimsky tag | DONE: 134 behaviors — 101 guarded, 23 write-test, 10 restore-feature (headliners: bundled binaries no longer read RIMSKY_AGENT_PORT contradicting the CLAUDE.md gotcha; parked-state 07-14 retirements [await_callback reason, max_park_duration, park-timeout watchdog] still in code with anti-guard tests; messages.* auto-subscribe edge injection [finding 1312] still present + anti-guarded; terminate doesn't cancel pending messages [finding 437]; backfill:create dossier item likely superseded) |
| sweep-4 | asset cascade-mode claim-tree error-policy inertness module-layout peer-auth role-template template | DONE (9/9): role-template, cascade-mode, module-layout, template, error-policy, inertness, peer-auth + producer-wire-errors child audits, plus asset (10 behaviors — 6 guarded, 2 write-test [CLI RunAsset* wrappers/flag-parsing + forward-lineage-from-asset acceptance untied], 2 restore-feature both likely-superseded artifact-only: asset backfill entirely unimplemented + asset-primary dashboard out of repo scope) and claim-tree (9 behaviors — ALL guarded; lone finding is dossier drift not loss: dossier says error_class `sibling_failed` but actual cancel cause is `sibling_cancel`, force-cancel itself fully guarded) |
| sweep-5 | atomic-staging cascade claim event-log instance named-lock permission run-scope terminal-resolution | DONE: 132 behaviors — 110 guarded, 15 write-test, 7 restore-feature (per-dossier files sweep5-*.json). Headliners: run-scope [2 rf] admin-terminate closes only root scopes not child sub-graph/partition scopes [07-17 ruling] + graceful frame settlement never closes root scope/fires OnRunScopeTerminal [transitionFrameEnd only stamps ended_at, 07-14 ruling]; cascade [1 rf] transient/retry+transient/infra+release_and_requeue+await_async still subscribable with taxonomy_test PINNING the drift vs 07-14 narrowing; instance [1 rf] force-terminate never cancels pending queued messages [CancelPendingForInstance exists but uncalled from terminate — finding 437]; event-log [2 rf] RevokeReasonExpired dead residue + permission_denied rows never set mode; atomic-staging [1 rf partial] example producer ships 1 of 4 promised scenario tests. permission dossier's TestRegistryCoversRouter [recorded as never-implemented] is NOW implemented — auth-before-every-handler gap closed) |
| sweep-6 | attribute child-execution conformance executor lifecycle-subscriber node-run persistence-database sensor terminal-tag | DONE: 114 behaviors — 91 guarded, 17 write-test, 6 restore-feature (sweep6-*.json). Headliners: executor [3 rf] Park.reason machinery + mid-dispatch scratch route POST /v1/runs/{id}/scratch both still live vs 07-14 erase rulings, + callback-advertise fail-fast MISSING [effectiveCallbackHostPort stamps wildcard bind host instead of refusing — genuine regression, not superseded]; attribute [1 rf] first-run attribute/<key>/changed diff uses empty-bag baseline but 07-14 ruling mandates schema-default baseline; node-run [1 rf] 07-17 claimant-guard ruling unimplemented, ""-sentinel carve-out still live + enshrined by conformance suite; conformance [1 rf] 2 numbered-invariant refs in auto_terminal_test.go violate no-numbered-ref rule w/ no lint, + executor conformance scenarios [tags_round_trip, async_callback_survives_restart, park_reason_emission] + blob suite have NO automated runner. child-execution + persistence-database fully guarded. Pattern: 5 of 6 rf are the newest user rulings [07-14 x3, 07-17 x1] not yet applied) |
| sweep-7 | auto-terminal claim-co-holdership control-api fan-out lineage-record node-subscription publisher-subscription service transition-reason | DONE: 126 behaviors — 112 guarded, 8 write-test, 6 restore-feature (sweep7-*.json). Headliners: control-api [3 rf] teardown fires roots-only run-scope fanout [CloseAndFanOutFrameRootRunScopesForInstance#320 — sub-graph/partition scopes die unfired, claims leak, 07-14 ruling] + nil-permissive auth gate survives [auth_middleware.go::gate#340 nil AuthState runs API fully ungated, ruled must-remove] + duplicate /diagnostics/parked route alias; service [1 rf] RIMSKY_AGENT_PORT contract unfulfilled — no bundled production binary reads it [claude-agent reads RIMSKY_EXECUTOR_PORT_GRPC], so rimsky run --service of any bundled binary spawn_fails, contradicts CLAUDE.md gotcha; lineage-record [1 rf] frame-trigger fields never plumbed [LeafRunEmitInput omits FrameTriggerKind/TriggerMessageID, finding 442]; publisher-subscription [1 rf] GET /instances/{id}/messages?sender=<publisher_name> filter absent. fan-out + node-subscription fully guarded [16/16 each]; finding-1312 auto-subscribe injection STILL PRESENT (edgeFromMessageRef in lib/graph/node/subscription_edges.go, anti-guard test pins it — sweep-3's report was right, this sweep's "confirmed removed" was wrong; corrected by the phase-3 dedupe audit 2026-07-19, row linked to 2517). Dormant-residue cleanup threads: HardDep* internal naming, retry_after_error/no-progress surface) |

## Ledger merge — DONE

Sweep findings merged into `review-findings-2026-07-06.csv` mechanically (script
`scratchpad/merge_sweep_findings.py`, never hand-edited): 259 rows, ids 2367–2625
(180 `direction=write-test`, 79 `direction=restore-feature`), each tagged
`source=phase2-sweep:<wave>:<dossier>`. write-test → `category=test-gap`;
restore-feature → `category=behavior` (or `design-drift-code-stale` when the
auditor flagged likely-superseded). Idempotency-guarded (aborts if phase2-sweep
rows already present). Pre-merge ledger backed up to
`/private/tmp/rimsky-phase2-backup/review-findings-2026-07-06.pre-merge.csv`.
NOT merged (held for a reconciliation pass, flagged not dropped): the standalone
`validation.json` deep-dive (46 behaviors, overlaps sweep-0's `validation`
dossier) and the four `*.md` child-audit notes (producer-wire-errors,
http-node-errors, claude-agent-error-classes, park-timeout-fanout).

## Adjudication — COMPLETE (2026-07-18)

All 79 restore-feature rows ruled: 72 EXECUTE (Phase-3 code queue), 5
RECONCILE-DOSSIER (backfill cluster per the 06-14 retirement + already-fixed
async-callback context), 2 new rulings (reason-style cause-string family
sweep; cancel_siblings knob DELETED — strict always cancels, threshold-at-
full-count is the keep-running form). Rulings live in the worksheet + ledger
phase2_disposition/phase2_note columns.

Guard-wave filter run over the 180 write-test rows: 173 GUARD-NOW (surface
unaffected by rulings — ready for writer agents), 4 GUARD-WITH-CHANGE (guard
lands with the Phase-3 change it cites), 3 SKIP-ERASED (surface slated for
deletion; no guard ever — 2409, 2541, 2592). Classification in the same
ledger columns.

## Guard-test wave (write-test rows, Sonnet writers)

GUARD WAVE COMPLETE 2026-07-19: 173 rows cut into 17 clusters — ALL verified+staged (169 guards landed incl. the re-dispatched 2432; 2 reclassified SKIP-ERASED at verification [2458 + companion]; handful already-covered). SIX production bugs fixed by the wave: atomic-staging no-op Release staging-dir leak; conformance grpc:// scheme strip; ClaimTerminalRecord terminating-supervisor never recorded; sensor-http cursor-advance-before-emission observation loss; four hand-built terminal signals bypassing the typed builder; sqlite topic-kind false-positive guard. Originally: 173 GUARD-NOW rows cut into 17 clusters (manifests in scratchpad + backup
`phase2-clusters/guard-*.json`). Same discipline as the writer clusters:
agents leave changes unstaged, orchestrator verifies + stages per cluster.

| cluster | rows | status |
| --- | --- | --- |
| guard-controlapi-0 | 12 | verified+staged (12/12 guards: anonymous-mode 7-row block [bad-bearer 401, banner cadence, anon-cache TTL, dry-run note, nil created_by, ownerless proxy routing, dedup buckets] + api-key rotate-revoked 409 + created_by identity + auth-sweep wiring + asset forward-lineage story + executorless before_dispatch skip; additive fix: SchedulerConfig.AuthSweepInterval parameterized so sweep wiring is testable; each guard adversarially red-checked) |
| guard-controlapi-1 | 12 | verified+staged (11 guards + 1 already-covered leg. PRODUCTION BUG FIXED [2387]: ClaimTerminalRecord never recorded the terminating supervisor — both candidate sources nulled before write; new TerminatingSupervisorID field threaded through forensics + mirrored into openlineage subscriber lockstep. Dry-run register/MCP parity, pause 409s + accumulate-drain, parked-never-woken-by-messages, payload inertness, publisher message_type registry gate, materialized-receiver node_count split all pinned. RESIDUAL: 2574 watch cross-poll watermark + sqlite3-artifact legs unguarded — deterministic test intractable, noted on ledger row) |
| guard-controlapi-2 | 11 | verified+staged (9 guards + 2 already-covered. PRODUCTION BUG FIXED [2621]: sensor-http advanced LastHash BEFORE emission — a failed POST permanently dropped the observation; now advances only after success, matching the other three sensors. Also: named-lock unavailable-metric distinctness; async-pending holds frame open under deterministic settlement sweep; grant unknown-field forward-compat; agent-supervisor refuses all 6 debug write verbs; self-host ignores sibling rimsky.yml; claude-agent real system-prompt/mcp files read by real subprocess; unreachable-peer fails startup fast; no-name-branching AST lint; same-name-across-roles independence. Full-scenarios run timed out amid concurrent writer mutations — authoritative pass deferred to exit gate) |
| guard-graphvalidation-0 | 12 | verified+staged (8 guards + 4 already-covered: refresh-interval default, matcher primitive-only equality, producer_candidate_handle DB round-trip, breakpoint snapshot omits claim content, audit request_params byte-verbatim + never-stores-key-plaintext, validation pipeline forwards bytes verbatim, debug-operator vs agent-supervisor grant boundary, env-ref induces no cascade edge [structural-root sender-key nuance handled per decision]. No production bugs. DISCIPLINE NOTE: agent used git checkout to restore two role JSONs after mutation — verified zero loss, files untouched by any cluster) |
| guard-graphvalidation-1 | 5 | verified+staged (4 guards + 1 already-covered: commit-writeback schema violation routes to attributes/schema_failed; ValidateTemplate accumulates all errors no-bail [NOTE for 2384 erasure pass: fixture uses a frame_timeout violation — swap when erased]; compose-manifest TLS enum fork covered [the config fork already was]; claude-agent schema-gate-before-signoff-gate ordering proven both directions. No production bugs) |
| guard-misc-0 | 3 | verified+staged (3/3: publisher-role validation honored at registration; pause accumulates publisher messages + drains on resume [co-located with operator analog]; publisher-peer TLS mode proven at dial site both directions vs a real mTLS server. No production bugs; clean /tmp-copy restore protocol) |
| guard-persistence-0 | 9 | verified+staged (7 guards written incl. spill-discipline triple on both drivers + terminal-atomic-commit rollback + operator⊆producer capabilities fail-fast; 1 already-covered [2474, concurrent agent's atomic-staging store tests]; 1 flagged [2476 scratch-progress-bump rides the 2598-erased scratch route — re-home after erasure]; no bugs found) |
| guard-runtime-0 | 12 | verified+staged (11 guards + 1 already-covered: retention sweep e2e, held-subgraph member gate + comember-skip e2e, observability tx-wrap + never-mutates matrix, idempotent substitution-failure routing, dispatch-bag survives writeback, bagsEqual on-demand resolution, atomic multi-lock sorted/all-or-nothing, claims-not-stores directive. All mutations restored clean; no production bugs) |
| guard-runtime-1 | 12 | verified+staged (12/12: co-held claim rides the wire + opened-wins at ALL THREE alias-collision sites; held-subgraph any-failed bypass; claim-content inertness AST fitness function; claim-handle survives node_run deletion [ON DELETE SET NULL pinned cross-driver in conformance suite]; resolved rows excluded from orphan cutoff; held-run error-shield; empty-delta held settlement; forensic lock-holders read; lock-spec scope population; empty-scope selector fallback. 2373/2415 duplicate pair closed by one conformance test; no production bugs) |
| guard-runtime-2 | 12 | verified+staged (11 guards + 1 already-covered: claim_scope + send-vs-emit vocabulary fitness tests [shared repo-scan helper]; absorbed-entry error/park settles parent w/ no internal cascade; infra-error skips operator policy w/ default+override caps; empty-wake unification; self-edge readOnly carry-forward core proof; omitted error_policy stamped strict at registration; graphContainingNodeType fallback; IsEmptyWake in BuildAttributeDeps; no message-originated special cascade path; named-lock count driven by claim state only. No production bugs) |
| guard-runtime-3 | 12 | verified+staged (9 guards + 2 already-covered + 1 REJECTED at verification [2458's guard pinned the 2598-erased scratchStoreAdapter — removed, row reclassified SKIP-ERASED matching 2476]. Landed: undeclared named-lock rejection + lock-name auto-subscribe coverage; parked-holder no-preempt contention; node_run summary bucket mapping cross-driver; no synthesized single-value state; fan-out children reuse parent node row w/ verbatim defaults; max_runtime deadline-release branch; parked rows invisible to reaper cross-driver; reapers never touch API keys; crash→release→reclaim by different supervisor + proxy supervisor-identity-blind scan. FLAGGED: child_partition_key_e2e uses wall-clock deadline+sleep — added to determinism follow-up) |
| guard-runtime-4 | 12 | verified+staged 11/12 (PRODUCTION BUG FIXED [2397]: four call sites hand-constructed terminal/success signals bypassing BuildTerminalSuccessSignal — all rerouted through the builder + AST lint test bans the literal form. Also: parked-frame-hold instance non-terminal e2e; new-frame purity; park emits no work_completed; run-scope kind never stored [cross-driver + exact column set]; in-tx emission atomicity via fault-injection; settling_signal_type NULL-not-empty cross-driver; force-terminate audits ONE instance_terminated event; event payload oneof excludes signal-class kinds. Row 2432 NOT done — re-dispatched to a follow-up writer) |
| guard-runtime-5 | 12 | verified+staged (12/12: held-subgraph no-double-fire; exit-node normal leaf run; multi-caller scope separation e2e; async-ack DB-backed restart recovery e2e; deadline max_runtime-wins + tautological default test fixed + conformance round-trips real deadlines; health active-count on-demand; 250ms/200ms defaults via extracted pure resolvers; FOR UPDATE OF d concurrent-tx proof; CLI stdout vocab + verb-dispatch routing; operator tags ignored on create. 2490/2587 already-guarded) |
| guard-runtime-6 | 7 | verified+staged (7/7: cascade-message send survives later rollback; payload.tags-vs-declared_tags warning; wait-set topic_kind CHECK rejects 'event' on BOTH drivers — and fixed a false-positive sqlite guard that failed via a dropped column instead of the CHECK; park tags land in audit event [tags survive the 2524 reason-erasure — annotation channel]; terminal tags ephemeral, never persisted into node state; UpdateState writes zero audit events cross-driver; wait-set frame_id CASCADE + held-frame never reaped) |
| guard-services-0 | 12 | verified+staged (10 guards + 2 already-covered. TWO production bugs fixed: atomic-staging example server.Release() was a NO-OP — never called the store, leaking every uncommitted claim's staging dir forever [Release now aliases Abandon per doc]; conformance NewGRPCClient never stripped grpc:// scheme unlike every sibling. Executor conformance scenarios now run against a live in-process executor; blobbackend suite has its first runner tests; host-agent cwd + crash-recovery/SIGKILL-escalation proven. Row 2599 stale — callback mTLS already guarded) |
| guard-services-1 | 12 | verified+staged (12/12: CLI run --service autostart e2e with real binaries; service_bindings on GET instance; main-scope reap on real terminate incl. DELETE-site ordering [OnRunScopeTerminal before OnInstanceTerminated]; lifecycle fanout replay no-op; OnInstanceCreated carries bindings+owner key; enable_lifecycle gates on BOTH bundled stores; module-layout fitness quartet incl. protoc regen-diff. Finding 2603's premise was wrong — canary instance was never idle [harness auto-wakes roots]; rewritten via raw POST to test true create-is-idle. No production bugs) |
| guard-services-2 | 6 | verified+staged (6/6: configured-wins-over-bundled extracted from 3-way inline duplication into testable helpers + guarded all three; checks/spec Severity type-parity [placed in test tree per depguard]; peer-auth TLS already comprehensively guarded; all four sensors' idempotency-key composition pinned to exact values incl. etag→name fallback; sensor Dockerfile+Makefile image-entry fitness test [ledger's compose/helm mention matched nothing real — guarded what exists]; claude-agent Wolfi/nonroot/tini + scan-gate polarity pinned. No production bugs) |

## Phase-3 scope directive (user ruling 2026-07-18)

EVERY open ledger row gets fixed — including the ~1,038-row minor/nit tail
(idiom uniformity, dead code, misleading strings, DRY consolidations). Not
optional polish. Efficient shape: batch by KIND (one idiom sweep closes all
assertion-dialect rows; one DRY pass closes all copy-paste rows), not by row.
Phase-3 queue = 74 adjudicated executes + 82 phase-1 fix-code + 282 original
fix-code (needs closure audit) + 4 deferred guards + minor/nit tail, deduped.

## Exit gate

`make test-all` green; every dossier required-behavior guarded or queued as a
restore-feature ledger row; ledger + this file updated; all work staged.

**MET (2026-07-18).** `make lint` green (three fix-forwards: unreachable
Shutdown reordered in peer_dial_ordering_test.go, dead orEmptyMap deleted,
gofmt sweep). Full suites green across all five modules: root (incl. complete
test/scenarios), lib/foundation, lib/protocols, lib/services (zero FAIL),
examples. Core + service + test images rebuilt from current source (the earlier
stack pass had run against pre-bug-fix images), then the docker-stack suites
under lib/services/test/ re-run against the fresh images: zero FAIL. One
standing t.Skip: examples/sub-claim-payload e2e, citing ledger 2344/2366
(restore-feature, Phase-3 queue). All work staged.

## Adjudication + source-of-truth (user directive 2026-07-18)

Rulings are recorded in BOTH the worksheet
(`2026-07-18-restore-feature-adjudication.md`) and the ledger
(`phase2_disposition`/`phase2_note` columns, synced by
`scratchpad/sync_rulings_to_ledger.py` after each ruling). The workbench
artifacts (this file, the worksheet) are TEMPORARY: once execution completes
they are removed. The design corpus — concepts, stories, tensions/decisions
under `.ok-planner/design/` — is the source of truth, so every execution change
must fold its ruling into the relevant corpus doc (and the ledger row closes)
as part of the same change, not as a separate docs pass.
