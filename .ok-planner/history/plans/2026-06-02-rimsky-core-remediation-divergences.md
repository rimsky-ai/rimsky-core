# Rimsky Core Remediation — Divergence Report

Audit of the working tree against `.ok-planner/plans/2026-06-02-rimsky-core-remediation.md`
(and the spec `…-design.md`). This is a record of where the implementation
differs from what the plan literally said, including consequential choices the
plan left unspecified. It is not a critique and proposes no fixes.

Build (`go build ./...`) and `make lint` both pass on the audited tree.

Most passes (1, 3, 4, 8, 9, 12, 13, 14, 15, 16, 17) match the plan's literal
text and intent. The divergences below are concentrated in Passes 2, 6–7, 10,
11, and 18, plus several "Fix Every Bug You Find" expansions.

---

## 1. Pass 6/7 — held-subgraph detection rebuilt on `holds.from`, deleting the entire deps-walk machinery (not the additive change the plan described)

- **What the plan said:** Pass 6/Task 12 — "`HoldingSubgraphsForTemplate` / `IsHeld`: build subgraph members from each node's `Holds` block (acquirer + every `holds:`-declaring co-holder), **in addition to** `Inherits`." Pass 7/Task 13 then removes the `inherits:`-specific branches. The framing implies a `holds:` member-collection branch added alongside the existing inherits deps-walk, then the inherits branch excised.
- **What was implemented:** The `holds:`-based subgraph reads the acquirer node-type directly from `HoldsBinding.From` (`lib/foundation/spec/graphs.go:42`), so no transitive-deps reachability walk is needed to route a co-holder edge to its acquirer. The implementer deleted `ValidateInheritance`, `transitiveAncestors`, `upstreamNodeTypes`, the `acqsByAlias`/`ancestors` indexes, and the entire ambiguity-resolution apparatus in one pass (`lib/graph/node/inheritance.go` — the file shrank ~266 lines of diff; `import "fmt"`/`"sort"` collapsed to `"sort"` only). `HoldingSubgraphsForTemplate` now iterates `n.Holds` and keys directly on `hb.From + "|" + alias`. `ValidateTemplate` lost its `ValidateInheritance(spec, &res)` call (`lib/graph/node/template_validator.go:273`).
- **Inferred reason:** `holds:` carries `from:` explicitly, which makes the acquirer unambiguous at the source. The old `inherits:` model named only an alias and inferred the acquirer via a deps-reachability walk (with explicit ambiguity rejection); once `inherits:` is deleted, that whole machinery is dead. The result is correct and simpler than the plan's "additive then subtractive" two-step suggested, but the literal "in addition to `Inherits`" intermediate state never existed — Passes 6 and 7 were effectively fused into a single rewrite.

## 2. Pass 10 (E4) — supervisor DataProcessing-client dialing wired into `StartSupervisor`; a whole `DataProcessors` registry threaded through `runtime.Config` → `RunArgs` → `CallbackServer` (not in any task)

- **What the plan said:** Pass 10 (Tasks 19–21) framed E4 as "wire the candidate handle onto the wire": link the sub-claim to its child run (`fanout_dispatch.go`, `claim_handles.go`), then read the persisted `producer_candidate_handle` at leaf dispatch and set it on the `StoreHandle`. The plan's grounding says "the persisted candidate" already exists (the stub store mints one per `BeginCandidate`); no task touches `StartSupervisor`, `runtime.Config`, or `CallbackServer`.
- **What was implemented:** Beyond the planned wire-binding, the implementer added a `DataProcessors DataProcessingRegistry` field to `runtime.Config` (`lib/runtime/supervisor.go:176`), threaded it into `RunArgs` at `Start` and in `runLoop` (`supervisor.go:303,525`) and into `CallbackServer` (`lib/runtime/callback.go:179`, set into `RunArgs` at `driveTerminal` ~:542), and dialed the DataProcessing mix-in clients in `StartSupervisor` via `DialPublisherAndValidationRegistries(…, RemotePublishersConfig{})` with a matching `closeDataProcessors` shutdown hook (`lib/control/config/supervisor.go:150-191,216-251`).
- **Inferred reason:** For the E4 end-to-end test to mint a candidate at all, the supervisor must actually hold DataProcessing clients to call `BeginCandidate` per sub-claim and `Commit/AbandonCandidate` at terminal. The plan's grounding ("the persisted candidate" already exists) understated the gap — the supervisor wasn't dialing DataProcessing producers, so no candidate was ever minted in a real stack. This is a substantial, load-bearing scope expansion the plan did not anticipate; without it the planned wire-binding would carry an always-empty handle.

## 3. Pass 10 (E4) — leaf candidate-handle binding implemented in `runner_acquire.go`, not the plan's named files; new `bindLeafCandidateHandles` + `ListByNodeRun`

- **What the plan said:** Task 21 named `lib/runtime/runner.go` and `lib/runtime/runner_dispatch.go`: "At leaf acquisition, look up the sub-claim row by `node_run_id = cand.DispatchID` and carry its `ProducerCandidateHandle` onto the leaf's `AcquiredLock`."
- **What was implemented:** The new `bindLeafCandidateHandles` helper lives in `lib/runtime/runner_acquire.go:680-744` (a file the plan never named for Task 21), called from `tryAcquire`. It uses a new persistence accessor `ClaimHandleTable.ListByNodeRun` (`lib/foundation/persistence/claim_handles.go:191`, + sqlite/postgres impls) rather than a bespoke lookup. `runner.go` got only the `AcquiredLock.ProducerCandidateHandle` field; `runner_dispatch.go` got only the `makeClaimHandle` one-liner. The binding is best-effort (logs and leaves the handle empty on lookup failure) and filters rows by `parent_claim_handle_id != nil && len(ProducerCandidateHandle) > 0 && ProducerName != nil`.
- **Inferred reason:** Leaf acquisition happens in `tryAcquire` (runner_acquire.go), which is where the leaf's own dispatch id and its `AcquiredLock` slice are both in hand — the natural site, even though the plan listed runner.go/runner_dispatch.go. Adding `ListByNodeRun` as a first-class accessor (rather than a one-off query) is cleaner than the plan implied. Mechanism matches the plan's intent.

## 4. Pass 10 (E4) — `UpdateNodeRunID` repoint also fixes an unrelated partition-scope-closure bug (out-of-scope bug fix)

- **What the plan said:** Task 20 — repoint the sub-claim's `node_run_id` to the child run "so each sub-claim is resolvable from the leaf by `node_run_id = its own DispatchID`." Scope: making the candidate handle reachable.
- **What was implemented:** The same repoint (`lib/runtime/fanout_dispatch.go:301-318`) is documented as also correcting the `fanout_partition` RunScope closure walk in `auto_terminal_chain.go::resolveParentClaimChain`, which loads each sub-claim's run by `node_run_id` to find the partition scope to close — with `node_run_id` previously pointing at the parent (main-scope) run, that walk matched no partition scope. The implementer surfaced this as a pre-existing latent bug the repoint incidentally fixes.
- **Inferred reason:** "Fix Every Bug You Find" — the repoint was discovered to also be load-bearing for partition-scope closure, beyond E4's stated candidate-handle purpose. Recorded in the code comment, not deferred.

## 5. Passes 11/23 — `PruneOldRunsForRetention` switched off `s.q(nil)` to avoid a scheduler-tick panic (out-of-scope bug fix)

- **What the plan said:** Pass 11 (Tasks 22–24) — wire `SweepLineageRetention`/`SweepRunTreeRetention` into the tick and plumb the `retention:` config. No task mentions the persistence-layer prune query itself.
- **What was implemented:** Both `lib/foundation/persistence/postgres/frames.go::PruneOldRunsForRetention` (~:546) and the sqlite peer (~:119) were changed from `s.q(nil)` to run directly against `pool.Exec` / `db.ExecContext`. The added comments state `s.q(nil)` "would trip the no-nil-tx contract and panic the scheduler tick."
- **Inferred reason:** The retention sweep is a standalone, tx-less caller; wiring it into the tick (Task 23) would have panicked on the first run against the existing `s.q(nil)` path. The implementer fixed the persistence method rather than working around it — consistent with the rules' "fix the function, don't work around it." Not anticipated by the plan.

## 6. Pass 2 (#1) — cursor follow-loop redesigned with a per-poll `prevSeen` snapshot and a nested drain loop (more than "pass the token through")

- **What the plan said:** Task 4 — "delete the `cursor = fmt.Sprintf("%d", lastSeenID)` line. Maintain a `nextCursor string` assigned ONLY from `page.NextCursor`. On a partial page leave the cursor empty; the existing `e.ID <= lastSeenID` dedup guard suppresses already-printed rows."
- **What was implemented:** Both `RunInstanceEvents` (`cmd/rimsky/cli/instances.go:467-...`) and `RunWatch` (`cmd/rimsky/cli/watch.go:60-...`) were restructured into an outer poll loop + inner drain loop, and the dedup test was changed from `e.ID <= lastSeenID` to `e.ID <= prevSeen`, where `prevSeen` is a per-poll snapshot of the watermark taken before draining. The implementer's comment explains why: pages arrive newest-first ((occurred_at, id) DESC), so advancing the committed watermark mid-drain would suppress every older (lower-ID) event on subsequent pages of the same backlog. `lastSeenID` is only committed after the whole backlog drains.
- **Inferred reason:** The plan's stated `e.ID <= lastSeenID` guard is actually unsafe under newest-first keyset pagination across multiple pages — updating `lastSeenID` to the first (global-newest) event would skip all older events on page 2+. The implementer caught this and introduced the `prevSeen` snapshot. The plan's literal instruction would have re-introduced a dedup bug; the implementation diverges to be correct. Behavior (each event printed once, no 500) matches the plan's goal.

## 7. Pass 16 (#9) — `ErrInvalidateConflict` semantics corrected, not just its text; its test updated to match

- **What the plan said:** Task 36 — "fix the genuinely-stale ones: … `admin_diagnostics.go` `ErrInvalidateConflict` text." Framed as a stale-string fix.
- **What was implemented:** The implementer changed both the sentinel text AND the described rule (`lib/control/controlapi/admin_diagnostics.go:334-340`): old text said invalidate "is valid only for parked or fresh states"; new text says it "is rejected only for a running node … parked / fresh / stale / failed nodes all accept the invalidate." `admin_diagnostics_test.go:408` was updated from asserting `"parked or fresh"` to `"running node"`.
- **Inferred reason:** Verifying the comment against the code (as Task 36 instructs) revealed the old text mis-stated the actual accept/reject set — only `running` is rejected. The implementer corrected the substance, not just the wording, and re-pinned the test. A larger correction than "stale text," but within Task 36's "verify each against current code" mandate.

## 8. Pass 18 — CI uses `make build-all`/`test-all`/`lint` (not `go build ./...`) and pins golangci-lint to v1.64.8

- **What the plan said:** Task 39 — add a workflow that runs "`go build ./...`, `go test ./...` …, and `make lint`. Pin the Go version to the repo's `go.mod`."
- **What was implemented:** `.github/workflows/ci.yml` runs `make build-all` / `make test-all` / `make lint` instead of bare `go build ./...` / `go test ./...`, with a comment explaining a root-only invocation under `go.work` silently skips the foundation/protocols/services submodules. It also pins `golangci-lint` to `v1.64.8` (the plan didn't specify a version), with a comment that the repo's `.golangci.yml` is a v1-style config that golangci-lint v2 (`@latest`) rejects. Go version is pinned via `go-version-file: go.mod` as the plan asked.
- **Inferred reason:** `go build ./...` at the repo root does not cover the four-module workspace, and `golangci-lint@latest` (v2) would fail against the v1 config. The implementer substituted the Makefile multi-module targets and a pinned lint version to make CI actually green and actually cover all modules. A correctness-driven deviation from the literal commands.

## 9. Pass 11 — retention loader uses pointer fields (absent vs. explicit-zero) and rejects negative durations (an unspecified design choice)

- **What the plan said:** Task 24 — "Apply the documented defaults in the loader so retention is on by default." No mention of how absent-vs-zero is distinguished or what defaults to use.
- **What was implemented:** `lib/control/config/stores.go` defines a `yamlRetention` struct with pointer fields (`*int`, `*time.Duration`) so the loader distinguishes an absent key (→ apply default) from an explicit `0` (→ disable that sweep), plus `parseRetention` rejects negative values. Concrete defaults were chosen: `recent_frames_kept: 100`, `lineage_trailing: 30d`, `claim_handles_trailing: 30d`, `message_idempotencies_trailing: 24h`.
- **Inferred reason:** "Defaults on by default" leaves open how an operator turns a sweep *off*; the pointer-field absent/zero split is a reasonable resolution (explicit `0` = disable). The specific default magnitudes are the implementer's call (mirroring the doc on `runtime.RetentionConfig`). Consequential but unspecified; recorded as a design choice the plan delegated.

## 10. Pass 16 (#36) / F8 ride-along — "supervisor startup" → "control-api startup" doc fixes spread beyond the plan's named files

- **What the plan said:** Pass 13/Task 28 fixes the resync doc in `lib/runtime/publishers.go`. Pass 16/Task 36 names a fixed file list (`TRADEMARKS.md`, `claim_holders.go`, `admin_diagnostics.go`, `auto_terminal.go`, `CLAUDE.md`).
- **What was implemented:** The "resync runs at supervisor startup → control-api startup" correction was also applied to `lib/foundation/persistence/publisher_subscriptions.go:10`, `lib/services/sensors/sensor-cron/sensor.go:25`, and `lib/services/sensors/sensor-http/state_db.go:11,19` — none of which appear in any task's file list.
- **Inferred reason:** Those doc strings all repeated the same now-false "supervisor startup" claim that Task 28 corrects in publishers.go; leaving them would re-strand the same drift the pass exists to remove. Consistent with the fix-every-instance discipline, beyond the enumerated files.

## 11. Pass 16 (#36) — conformance-doc Notes entry dated 2026-05-27 but attributed to the 2026-06-02 spec

- **What the plan said:** Task 33 — rewrite `conformance.md` to the subcommand model, "Update … naming-history Notes."
- **What was implemented:** The appended Notes entry in `.ok-planner/design/concepts/conformance.md` reads `2026-05-27 — the standalone per-protocol conformance binaries were folded into "rimsky conformance <protocol>" subcommands … Per spec:2026-06-02-rimsky-core-remediation-design.` The date (2026-05-27, when the fold actually happened per `module-layout.md`'s history) does not match the citing spec's date (2026-06-02). The `_retired/sdk.md` and `module-layout.md` Notes entries use the current `2026-06-02` date for their drift-correction entries.
- **Inferred reason:** The implementer dated the entry to when the *change being documented* occurred (the 2026-05-27 reorg) rather than to the remediation run that *recorded* it. A minor internal inconsistency in Notes-entry dating convention across the three touched concept files; does not affect correctness.

## 12. Plan-wide — existing `inherits:` scenario tests and harness helpers migrated to `holds:` (collateral of Pass 7, not enumerated)

- **What the plan said:** Task 13 step 5 — "Remove now-dead helpers/tests that referenced `Inherits`." Generic; no file list.
- **What was implemented:** Five existing scenario tests were migrated from `scenario.WithInherits(scenario.Inherit(...))` to inline `Holds: map[string]node.HoldsBinding{...}` (`held_claim_acquirer_passes_test.go`, `held_claim_acquirer_blocked_pass_test.go`, `held_claim_mixed_upstream_test.go`, `parked_lifecycle_test.go` (two cases), `claim_stores/auto_terminal_aggregate_outcome_test.go`), and the `WithInherits`/`withInherits`/`Inherit` harness helpers were deleted from `test/support/scenario/harness.go` and `harness_util.go` (including the `inherits` key emission in `templateNodeToJSON`).
- **Inferred reason:** Deleting the `Inherits` field forces every test that exercised held claims via `inherits:` to re-express them via `holds:` (with explicit `from:`), and removes the now-dead harness sugar. This is the expected fallout of Task 13; recorded here only because the migrated test files were not individually named in the plan and the helper removal is part of the public scenario-harness surface.
