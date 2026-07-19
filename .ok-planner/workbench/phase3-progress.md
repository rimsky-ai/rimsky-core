# Phase 3 — fix everything (drift-remediation, final phase)

Started 2026-07-19 from commit 182b314d. Sequencing approved by user:

1. Erasure sweeps (ratified deletions first — they moot defects in doomed code)
2. Canonical executes by subsystem cluster (fold ruling into design corpus in the same change)
3. ~239 distinct open defects in area batches (per-row current-code evidence in ledger phase3_note)
4. Dossier reconciliations + guard-with-change tests, riding their owning clusters
5. Minor/nit tail by-kind sweeps (~1,424 rows incl. dup riders)
6. Close-out: recorded follow-ups, final all-rows-closed re-audit, corpus true-up, workbench teardown

Ledger: `review-findings-2026-07-06.csv` (phase3_status/phase3_note columns; scripts only, never hand-edit).
Exit gate per phase-2 standard: make lint green, all five modules green, stack suites green on
freshly built images, every ledger row closed or refuted, corpus is source of truth, workbench removed.

## Step 1 — erasure packets

| Packet | Ledger rows | Scope | Status |
|---|---|---|---|
| E-proxy-trim | 2463 | delete proxy late-bind publisher/validation/data-processing handlers; loud rejection; anti-guard | DONE: three handlers + dead forwarding deleted (proxy AND hostagent side); Unimplemented fail-loud proven by new scenario + unit guards; stubchild trimmed; corpus already post-erasure (b7e34819). Closes riders 83/84/323. Verified, staged. |
| E-empty-claimant | 2605 | remove ''-sentinel carve-out from both queue drivers' claimed_by guards; explicit force variant | DONE: bypass branch deleted in both drivers; four explicit Force* verbs added; two admin callers switched; conformance ClaimantGuard/RunForceOverride proves empty claimant = wrong claimant on both backends; node-run.md invariant tightened. Closes rider 1030. Verified, staged. |
| E-controlapi-auth-routes | 2618, 2619 | remove nil-AuthState branches (fail loudly at construction); delete /diagnostics/parked alias, one-route-per-action pin | DONE: NewApp panics on nil AuthState (5 permissive branches swept incl. mcp_route/enroll); alias deleted; 44-action route-count pin test; control-api.md invariants added. Integration fixups by lead: 11 residual sites (RefValidationMode wiring x3, InsertRunningFrame arity x11 calls, frame_timeout_ms raw-SQL fixtures x6 files) fixed forward; full controlapi suite green; repo build green. Staged. |
| E-auto-subscribe | 2517 | remove messages.* auto-subscribe edge injection; explicit subscriptions only; inverse guard | DONE: injection loop + edgeFromMessageRef deleted; anti-guard retired, inverse guard added + red-checked; all fixtures already explicit (ref-coverage validator made injection redundant); corpus already correct, no doc edits needed. Verified, staged. |
| E-frame-timeout | 2384 | drop frame_timeout_ms + CHECK (migration), delete validateFrameTimeout + runtime path | DONE: migration 024 both backends (sqlite table-rebuild); validateFrameTimeout + stuck-frame sweep + persistence surface deleted; ~60-file fallout cleaned; 3 pinning scenario files retired; unknown-key 400 guard added; 2 decision docs corrected. RESIDUAL in locked controlapi: frames.go FrameTimeoutMs + messages_test 5-arg InsertRunningFrame. Staged. |
| E-cancel-siblings | 2456 | delete cancel_siblings knob; strict always cancels; threshold-at-full-count is keep-running form | DONE: field erased from spec/persistence/runtime/validator; strict cancels unconditionally; unknown-key 400 guard; threshold-at-full-count keep-running guards added both domains; corpus (cancel-siblings/claim-tree/claim-co-holdership) rewritten. Runtime suite re-verify deferred to post-park integration gate (proto regen in flight). Staged. |
| E-ref-validation-mode | 2470 | erase ref_validation_mode (all modes) + execSchemaVisible + deps wiring; strict per-name validation unconditional | DONE (one residual): knob + env + templates: YAML block erased (unknown-key rejection, no redirect hint per 2407); strict path inverse-guarded; scenario/e2e fallout fixed on merits; template.md invariant rewritten. RESIDUAL: 3 RefValidationMode wiring sites in lib/control/controlapi (app.go/templates.go/templates_test.go) await the in-flight controlapi agent — integration fix on its landing. Staged except controlapi. |
| E-transient-subscribe | 2568 | narrow subscribable set to settling types (ShouldCascade); inverse guard: subscribing to transient = validation error | DONE: ShouldCascade(TypePath) in signal taxonomy is sole authority; subscribable set DERIVED from it (twin list dead); runtime cascade walk gates on same predicate; inverse guards at taxonomy + validator; cascade/node-subscription/signal corpus updated. Signal pkg staged; runner_terminal + validator test staged after frame-timeout lands. |
| E-park-cluster | 2385, 2526, 2524, 2597, 2551(reason leg), 2598 | park watchdog erasure; ParkReason erased (Park = resume_at required + scratch + tags); scratch route deleted | DONE: all 3 legs; proto fields reserved + regen; migration 025 both backends (incl. dead parked_resume_at); park_timeout transition illegal-guarded; conformance park_emission replaces reason scenarios; http-node 429 tags rate_limited; claude-agent report_park reshaped; scratchStoreAdapter was still live (phase-2 note wrong) — now truly erased; corpus swept (8 docs). Closes riders 1478/1656. Staged. |
| E-numbered-invariants | 2594 | remove numbered-invariant refs in auto_terminal_test.go; add lint keeping them out | DONE: 4 offenders fixed (3 in auto_terminal_test.go + 1 found by sweep in claim_content_inertness_test.go); TestNoNumberedInvariantReferences fitness test added + red-checked. Verified, staged. |
| E-delegation-fanout | 2417 | remove validator prohibition: fan-out partition node can run a delegated sub-graph | DONE: prohibition removed AND real runtime bug fixed (SettleFromDelegate never released the calling node's own claims — partition sub-claims stuck active forever); red-checked e2e proof (3 partitions, per-partition sub-graph scopes, all claims commit); delegation.md + fan-out.md updated. Closes rider 59. Staged. |
| E-pure-erasure-guard | 2407 | LAST: guard that retired keys fail as UNKNOWN everywhere (no redirect/directing-error paths) | DONE: 3 surviving redirect hints from EARLIER retirements (stores, write_semantics, write_semantics_envelope) found + genericized; table-driven guards (config: 6 keys, registration: 4 keys) with forbidden-word assertions, red-checked; Park reserved-fields descriptor test. Closes rider 2280. Staged. |

Writer discipline (carried from phase 2): shared working tree — waves of ≤4 on disjoint areas;
ABSOLUTE BAN on git checkout/restore/reset/stash/clean; fix forward only; byte-copy to /tmp before
any temporary mutation and restore by copy-back; agents do NOT stage, commit, or touch the ledger;
I verify each packet (build + affected tests + red-check where applicable), stage, then refill.
Pre-v1: migrations drop/recreate freely; dev DBs may be nuked (say so in the report).

**Step 1 gate MET (2026-07-19).** make lint green; root module full suite green (incl. complete
test/scenarios); foundation, protocols, examples, services modules green — the final examples +
services docker-stack legs ran against images rebuilt from the exact committed source. Three gate
regressions found and fixed forward during verification (strict-aggregate signal overwrite,
lifecycle-subscriber wiring lie, dead parked-alias callers incl. the CLI verb).

## Divergence / discovery log

(items surfaced during execution land here)

- Aggregation-threshold off-by-one between run_tree.go (failures >= max) and child_execution.go (abandoned > max) rediscovered during 2456; already tracked as ledger 1526 (CONFIRMED, OPEN) — cancel-siblings guards written to current semantics; 1526 fixes the divergence in its own batch.

- runtime_diagnostics_e2e_test.go transient leg: RESOLVED — retargeted to a settled-signaler terminal wait-set row + blocked-inheritor wedge; green in 3s. (Post-2568 undrained wait-set rows no longer occur: transients cannot cascade, and terminal rows drain in the settle transaction.)
- NEW (from 2417): delegate-with-claims FAILURE path never releases the caller's claims (success path fixed in-packet; failure/abandon path latent leak in runner_error_policy) — dispatch as its own fix packet in step 2.
- NEW (from 2417): plain fan-out double-acquires per partition (split sub-claim + direct re-resolve; harmless, wasteful) — queue with the minor sweeps.
- Erasure exposed a wiring lie in examples/lifecyclesubscriber: harness declared the subscriber as an EXECUTOR while templates referenced it as a STORE — only the erased ref-validation leniency let that register. Fixed to the blessed shape per concept:lifecycle-subscriber (claim-producer primary + lifecycle protocol alongside; fake + reference binary now serve ClaimProducer Capabilities); e2e green.
- GATE REGRESSION (fixed): unconditional sibling-cancel made the claim family resolve inside the first child's terminal tx, so the deferred-held poison write stamped terminal/error/abandoned on the fan-out parent run before the run-domain aggregate could project strict_failed (children still [running,stale,stale] at poison time — verified by trace). Fix: walkUpwards now corrects the settling signal on an already-settled parent when the aggregate verdict differs (row correction only, no re-cascade). Deterministic 3x green + blast-radius suites green. The deeper claim-domain vs run-domain verdict unification remains the kill-path cluster's job (2379/2617/2380).
- GATE HANG (fixed): TestClaudeAgentCrossStack's parked-wait helper polled the DELETED /v1/diagnostics/parked alias (404 forever = designed loud hang). Sweep found two more stragglers outside the controlapi agent's module scope: mcp_transport_parity_e2e and the rimsky parked CLI verb (a real production bug — the shipped CLI called the dead route). All three now use /v1/admin/diagnostics/parked-nodes; cross-stack test green in 10s, full CLI suite green.
- Re-run at integration gate: TestTemplateFanOut_AbandonPropagatesToParentError + TestFanOutChildren_ReuseParentNodeRow_UserdataVerbatim (failed transiently amid concurrent in-flight diffs, attributed not-caused; verify on quiet tree) and lib/runtime/hostagent full suite (601s hang seen once under load).
