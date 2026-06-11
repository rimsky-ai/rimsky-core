# Cascade and claim-handoff — completion report

**Spec:** `.ok-planner/specs/2026-06-10-cascade-and-claim-handoff-design.md`
**Plan:** `.ok-planner/plans/2026-06-10-cascade-and-claim-handoff.md`
**Date:** 2026-06-10

The plan ran to completion. The verification gate is green (`make build-all`,
`make lint`, `make test-all`) — one downstream environment fix landed in the
Makefile to close a pre-existing parallel-load flake in `lib/services` sensor
tests (see Section 3). The no-deferral audit is clean.

This report walks every spec-manifest item: each story is exhibited working,
and every implementation choice (the spec carries no new technical decisions
of its own) is enumerated as a divergence under the necessity rule.

---

## 1. Proof walkthrough

### STORY-claim-handoff — co-holder reads `{{claim.<alias>.<field>}}` at dispatch; auto-terminal Commit/Abandon fires across the holding subgraph

- **Artifact:** `test/scenarios/claim_handoff_e2e_test.go`
- **Invocation:** `go test -count=1 ./test/scenarios -run TestClaimHandoff_E2E`
- **What it exhibits:** Five subcases under `TestClaimHandoff_E2E`:
  - `A_RegressionClose` — acquirer + co-holder reading `{{claim.X.address}}` →
    Commit; closes the GH-issue-#16 regression.
  - `B_PerFieldSubstitution` — `.address`, `.payload.<f>`, `.claim_scope` each
    resolve to the held claim's bytes.
  - `C_AbandonPath` — co-holder forced to `terminal/error/<class>` via
    `error_types: give_up` → Abandon; `claim_handle.state = abandoned`.
  - `D_MultiCoHolderCommit` — two co-holders; `claim_handle.state` stays
    `active` until both settle, then transitions to `committed` (atomicity).
  - `E_WirePayloadParity` — co-holder's substituted address bytes are
    byte-equal to the acquirer's persisted `claim_handle.Address`.
- **Status:** EXHIBITS WORKING.

### STORY-claim-handoff-across-frames — held claim survives the frame boundary; substitution resolves to the same bytes in the new frame

- **Artifact:** `test/scenarios/claim_handoff_across_frames_e2e_test.go`
- **Invocation:** `go test -count=1 ./test/scenarios -run TestClaimHandoff_AcrossFrames`
- **What it exhibits:** Three variants under `TestClaimHandoff_AcrossFrames`:
  - `V1_FrameNextPerNodeSubscription` — co-holder with `Frame: "next"`;
    distinct `frame_id` rows; `claim_handle.state = active` across the
    boundary; transitions to `committed` only after the co-holder settles.
  - `V2_InstanceTrueCrossCutting` — `Instance: true` subscription
    (defaulted to `Frame: "next"`); same three properties asserted.
  - `V3_ThreeFrameChain` — acquirer + two `Frame: "next"` co-holders, three
    distinct `frame_id` values, held row remains `active` until the third
    frame ends, substituted address bytes equal acquirer's on every hop.
- **Status:** EXHIBITS WORKING.

### STORY-claim-handoff-durable — `lifetime: durable` row survives past the dispatch terminal; future dispatches can co-hold it

- **Artifact:** `test/scenarios/claim_handoff_durable_e2e_test.go`
- **Invocation:** `go test -count=1 ./test/scenarios -run TestClaimHandoff_Durable`
- **What it exhibits:** Five subcases under `TestClaimHandoff_Durable`:
  - `A_CrossDispatchPersistence` — durable row reaches `state = committed`
    in D1; forced `runtime.SweepClaimHandleRetention` tick; row is still
    present (durable-exemption invariant).
  - `B_CrossDispatchHolds` — D2 co-holder declares `holds:` against D1's
    alias; substituted `{{claim.<alias>.address}}` bytes equal the
    persisted `claim_handle.Address`; D2 settles fresh.
  - `C_ConflictDetectionIncludesCommittedDurable` — second template's
    acquirer against the same scope hits `terminal/error/acquire/unavailable`
    while the durable row is committed.
  - `D_AssetReleasePath` — `DELETE /v1/instances/{id}/assets/{alias}`
    removes the row from the active-scope set; subsequent acquirer
    succeeds.
  - `E_InstanceTerminationRelease` — `POST /terminate` then `DELETE
    /v1/instances/{id}` invokes `ReleaseHeldDurableClaims`; the
    `claim_handle` row and the instance row are both gone.
- **Status:** EXHIBITS WORKING.

### STORY-cascade-signal-blind — subscribers fire on every cascade-firing signal type

- **Artifact:** `test/scenarios/cascade_signal_blind_e2e_test.go`
- **Invocation:** `go test -count=1 ./test/scenarios -run TestCascadeSignalBlind_E2E`
- **What it exhibits:** Nine subtests under `TestCascadeSignalBlind_E2E`
  walking every cascade-firing signal type from the canonical taxonomy:
  - `terminal_success__{per_sender,cross_cutting}` (2 rows)
  - `terminal_error_giveup__{per_sender,cross_cutting}` — the GH-issue-#15
    regression-close per-sender row plus its cross-cutting twin (2 rows)
  - `terminal_error_pass__{per_sender,cross_cutting}` — the fresh-color
    terminal-error settlement under `error_types: pass` (2 rows)
  - `transient_retry__per_sender` (1 row)
  - `attribute_changed__per_sender` (1 row)
  - `event_named__per_sender` (1 row)
  Each row drives a real settlement through the runtime (no hand-injected
  signals), asserts subscriber dispatch, and asserts the audit row lands
  in `rimsky_events`.
- **Status:** EXHIBITS WORKING.

---

## 2. Technical decisions kept

No technical decisions in the spec's manifest; the spec's `### Technical
decisions` section reads "None. The spec delivers existing intent; no new
architectural choices." All implementation choices the implementer made
under the necessity rule land in Section 3.

---

## 3. Technical decisions diverged

The spec stated the work delivers existing intent and explicitly left
implementation choices unenumerated, on the framing that the proofs would
either pass against existing code or compel a fix per the necessity rule.
Six implementation choices were necessitated by stories failing to hold
without them, and one was necessitated by the verification gate. Each is
documented below.

### 1. `payload` column on `rimsky_claim_handles`, plus the postgres + sqlite 008 migrations, the `Payload` field on `ClaimHandleRow`, and the `UpdatePayload` claim-handle method — **necessitated**

- **What the spec said:** Spec named `{{claim.<alias>.payload.<f>}}` as one
  of the three substitution kinds STORY-claim-handoff must resolve, but
  did not call out that the bytes had to survive past the acquirer's
  acquire-tx. The spec assumed existing code already delivered the
  outcome.
- **What was implemented:** New `payload` column on `rimsky_claim_handles`
  via `lib/foundation/persistence/postgres/migrations/008-claim-handle-payload.sql`
  and `lib/foundation/persistence/sqlite/migrations/008-claim-handle-payload.sql`;
  new `Payload json.RawMessage` field on `persistence.ClaimHandleRow`
  (`lib/foundation/persistence/claim_handles.go:42`); new
  `UpdatePayload(ctx, id, supervisorID, payload, tx)` method on
  `ClaimHandleTable` with claimant-guarded postgres and sqlite
  implementations (`lib/foundation/persistence/claim_handles.go:198`,
  `lib/foundation/persistence/postgres/claim_handles.go:120`,
  `lib/foundation/persistence/sqlite/claim_handles.go:125`).
- **Reason:** Without the persisted column, a co-holder's
  `loadInheritedClaimsForNode` reads a row with `Payload = nil` at its own
  acquire-tx and the `{{claim.<alias>.payload.<f>}}` substitution drops to
  `ErrMissingSource`, sending the dispatch to
  `terminal/error/template_resolution_failed`. The plan's STORY-claim-handoff
  subcase B (per-field substitution) requires the bytes to survive
  cross-tx.

### 2. `evaluateClaimScopeConflict` persistent-vs-active split in `lib/runtime/runner_acquire_claims.go` — **necessitated**

- **What the spec said:** Spec said a competing acquirer against a
  committed-durable scope must settle `terminal/error/acquire/unavailable`,
  but didn't name the code-path change that delivers it.
- **What was implemented:** `evaluateClaimScopeConflict` now returns
  `(conflicted, persistent, err)` instead of `(conflicted, err)`; on a
  conflict the function marks `persistent = h.State ==
  ClaimHandleStateCommitted && h.Lifetime == ClaimLifetimeDurable`
  (`lib/runtime/runner_acquire_claims.go:334`). The caller routes
  `persistent=true` through `openResultUnavailable` so the operator's
  `error_types: { acquire/unavailable: ... }` chain fires; `persistent=false`
  keeps the legacy retry-bail shape for still-in-flight holders
  (`lib/runtime/runner_acquire_claims.go:101`).
- **Reason:** STORY-claim-handoff-durable subcase C requires a competing
  acquirer against a committed-durable row to surface as
  `terminal/error/acquire/unavailable`. Before the split, every conflict
  routed to `openResultBail`, which retried-then-bailed forever — the
  asset surface never releases on its own, so the scheduler tick would
  silently stall the conflicting node.

### 3. `Payload` propagation in `lib/runtime/runner_locks.go::collectCoHeldClaims` — **necessitated**

- **What the spec said:** Spec required the per-field
  `{{claim.<alias>.payload.<f>}}` substitution to resolve on co-holders;
  did not name `collectCoHeldClaims` as load-bearing.
- **What was implemented:** `collectCoHeldClaims` now passes the persisted
  `lh.Payload` into the synthesized `claimproducer.ClaimResult` alongside
  `Address` and `ClaimScope` (`lib/runtime/runner_locks.go:244`).
- **Reason:** Without the propagation, the co-holder's substitution context
  has the column read back from persistence (item #1 above) but never
  forwards it into the `ClaimResult` the substitution grammar reads from.
  The per-field substitution then fails on the co-holder side.

### 4. `--terminate-after-run` CLI flag on `rimsky run` plus `CreateInstanceRequest.TerminateAfterRun` wiring and the onboarding-demo.sh update — **necessitated**

- **What the spec said:** Nothing; the CLI surface is out of the spec's
  scope. The work surfaced an existing bug.
- **What was implemented:** New `--terminate-after-run` flag on `rimsky run`
  (`cmd/rimsky/cli/run.go:105`), new `TerminateAfterRun bool` field on
  `CreateInstanceRequest` (`cmd/rimsky/cli/client.go:485`); `--no-keep`
  implies `--terminate-after-run` so the dev-loop verb stays coherent
  (`cmd/rimsky/cli/run.go:130`); `examples/onboarding-demo.sh` passes
  `--terminate-after-run` so `rimsky watch` can exit
  (`examples/onboarding-demo.sh:64`).
- **Reason:** `TestOnboardingDemo_RunReachesTerminal` (under
  `lib/services/test/scenarios/`) was exposing that the durable-by-default
  instance lifecycle keeps `terminated_at` NULL forever unless explicitly
  opted in. The demo's `rimsky watch` polled forever. This is a bug per
  the project's "Fix Every Bug You Find" rule, so the fix landed in this
  plan rather than being deferred.

### 5. `cmd.WaitDelay` defensive fix on `runDemoScript` in `lib/services/test/scenarios/onboarding_demo_e2e_test.go` — **necessitated**

- **What the spec said:** Nothing.
- **What was implemented:** `cmd.WaitDelay = 5 * time.Second` set on the
  bash subprocess inside `runDemoScript`
  (`lib/services/test/scenarios/onboarding_demo_e2e_test.go:205`).
- **Reason:** Without `WaitDelay`, `exec.CommandContext`'s SIGKILL on
  context cancel does not close the inherited stdout/stderr pipes; a
  grandchild (`rimsky watch` mid-poll) keeping the pipe open blocks
  `cmd.Run` past the test's own 180s timeout and escalates to the
  package-level 10m timeout panic, consuming the rest of the
  `make test-all` budget. The defensive fix surfaces the timeout
  cleanly as the test's own failure.

### 6. `-parallel 4` cap on `lib/services` tests in the Makefile — **necessitated by the verification gate**

- **What the spec said:** Nothing.
- **What was implemented:** `cd lib/services && go test -parallel 4 ...`
  in `make test-all`, with a comment block explaining the pre-existing
  parallel-load flake (`Makefile:71`).
- **Reason:** `lib/services` integration tests spin up their own
  rimsky-all-in-one + sensor + state-postgres + ryuk reaper per
  `t.Parallel()` subtest. At GOMAXPROCS-wide parallelism that's 40+
  containers fighting for CPU / disk / network on a modern host;
  rimsky's synchronous Subscribe handshake (~2.36s retry budget) pushes
  past sensor-state-DB poll deadlines, surfacing as non-deterministic
  "sensor never persisted subscription within 90s" failures. NOT
  spec-directed — this is a verification-gate fix so our work's gate
  passes deterministically. The cap bounds concurrent-stack count; the
  wall-clock cost is minor because most tests are docker-IO-bound, not
  CPU-bound.

---

## Coverage check

- **Stories exhibited:** 4 / 4 in the spec's manifest.
- **Technical decisions in the spec's manifest:** 0 (the spec delivered
  existing intent and explicitly carried no new TDs).
- **TDs kept:** N/A (no spec TDs to keep).
- **TDs diverged (necessitated under the necessity rule):** 6 (the
  payload-persistence column + propagation, the persistent-vs-active
  conflict split, the CLI `--terminate-after-run` flag, the
  `cmd.WaitDelay` defensive fix, and the `-parallel 4` Makefile cap).
- **Design changes:** 4 new stories under `.ok-planner/design/stories/`
  (claim-handoff, claim-handoff-across-frames, claim-handoff-durable,
  cascade-signal-blind), all carrying the spec body verbatim with `status:
  as-is` frontmatter; plus the in-place sharpen of the `serial_queue`
  bullet in `.ok-planner/design/concepts/frame.md`.
