# Post-Data-Platform Cleanup Milestone — Design Sketch

**Date:** 2026-05-17
**Status:** Sketch (not a spec; not authorization to build)

## Idea

After the 2026-05-15 data platform extensions delivery (17 dispatches), the 9-cycle cleanup loop (46 findings closed), the A+B forensics follow-up (lineage `outcome` column + 7 new event-emit sites), and the inline architecture review, several items remain. They span paydown (cold-read file splits), refactor (claim-handle state column to collapse the held-durable guard network), sharpening (smoke flake investigation, multi-replica sensor-cron coverage), and discipline (a deferred review pass on the A+B work and end-to-end coverage uniformity).

The user prefers larger planning milestones over many small ones. This omnibus captures the six items as a single follow-up that one brainstorm session can scope into one or two specs. The items are individually tractable; their grouping is for planning convenience, not execution coupling. The state-column refactor (item 1 below) is the largest and most consequential; the rest are smaller and could ride alongside or follow.

## Shape

Six items, ordered roughly by their natural sequence (cheapest discipline first, biggest refactor in the middle, residual cleanup at the end).

---

### Item 1 — Claim-handle state column refactor (large)

**The biggest of the six.** Replace the `held_durable bool` column on `rimsky_claim_handles` with an explicit `state` column (`'active' | 'committed' | 'abandoned' | 'released'`), parallel to the run-row-lifecycle cutover that landed in cycles 8–10 for `rimsky_node_runs`. Collapses the ~10 `HeldDurable` consultation sites across runtime + persistence into "the state column is the discriminator" everywhere. Makes the durable-claim contract structural rather than discipline-based.

#### Schema

```sql
ALTER TABLE rimsky_claim_handles
    ADD COLUMN state TEXT NOT NULL DEFAULT 'active'
    CHECK (state IN ('active','committed','abandoned','released'));

-- After Go-side cutover:
ALTER TABLE rimsky_claim_handles DROP COLUMN held_durable;
```

States:

| state | meaning | claimed_by | row deleted? |
|---|---|---|---|
| `active` | currently held by a supervisor, heartbeating | NOT NULL | no |
| `committed` | producer Commit fired; row preserved | NULL | by retention sweep |
| `abandoned` | producer Abandon fired (natural or force-cancel); row preserved | NULL | by retention sweep |
| `released` | producer Release fired (instance terminate or operator DELETE) | NULL | by retention sweep |

`subgraph`-lifetime claims live: `active → committed → (delete at retention)` or `active → abandoned → (delete at retention)`. Today they're Deleted at terminal; under the refactor they transition through `committed` / `abandoned` first and the Delete is deferred to a retention sweep (parallel to run-tree retention).

`durable`-lifetime claims live: `active → committed → released → (delete at retention)`. The `released` state is the brief window between `ReleaseHeldDurableClaims` firing and the row being retention-swept; surfaces post-release diagnostics ("did the Release verb actually fire? was the producer reachable?") without keeping the row around forever.

CHECK constraints (defense-in-depth):

```sql
CHECK (state != 'active' OR holder_supervisor_id IS NOT NULL);  -- active must have a holder
CHECK (state = 'active' OR holder_supervisor_id IS NULL);       -- others must not
```

Partial indexes:

```sql
CREATE INDEX rimsky_claim_handles_active_idx
    ON rimsky_claim_handles (holder_supervisor_id) WHERE state = 'active';
CREATE INDEX rimsky_claim_handles_committed_idx
    ON rimsky_claim_handles (node_id) WHERE state = 'committed';
CREATE INDEX rimsky_claim_handles_visible_for_scope_idx
    ON rimsky_claim_handles (producer_name, scope_data_hash) WHERE state IN ('active','committed');
```

#### State transitions

| Transition | Site | Trigger |
|---|---|---|
| `(none) → active` | `runner_acquire.go::tryAcquire` (INSERT) | acquisition tx |
| `active → committed` | `terminal_decision.go::ResolveClaimHandleTerminal` (Commit branch) | producer Commit fired |
| `active → abandoned` | `terminal_decision.go::ResolveClaimHandleTerminal` (Abandon branch) | producer Abandon fired (natural or force-cancel) |
| `committed → released` | `instance_termination.go::ReleaseHeldDurableClaims` OR operator DELETE | producer Release fired |
| any terminal → (deleted) | retention sweep | retention window expired |

Illegal transitions return `ErrIllegalTransition` (mirror of `cascade.ErrIllegalTransition` for node-run states).

#### SQL transformation map

Every existing `held_durable` filter has a state-column equivalent. Same shape in postgres + sqlite drivers.

| Site | Today | Post-refactor |
|---|---|---|
| `ListByProducerScope` | `WHERE (expires_at > now() OR held_durable = TRUE)` | `WHERE state IN ('active','committed')` |
| `ExtendHeartbeat` | `WHERE held_durable = FALSE` | `WHERE state = 'active'` |
| `DeleteIfExpired` | `WHERE held_durable = FALSE AND expires_at < now()` | `WHERE state = 'active' AND expires_at < now()` |
| `ListHeldDurableByInstance` | `WHERE held_durable = TRUE AND n.instance_id = $1` | `WHERE state = 'committed' AND lifetime = 'durable' AND n.instance_id = $1` |
| `SetHeldDurable(id, true)` | `UPDATE … SET held_durable = TRUE` | `UPDATE … SET state = 'committed'` |
| `Delete` (terminal natural delete) | `DELETE FROM … WHERE id = $1` | `UPDATE … SET state = 'committed' OR 'abandoned'` |
| Recursive walk `if HeldDurable` skip | bool check | `if state != 'active' continue` |

#### Go-side surface

```go
type ClaimHandleState string

const (
    ClaimHandleStateActive    ClaimHandleState = "active"
    ClaimHandleStateCommitted ClaimHandleState = "committed"
    ClaimHandleStateAbandoned ClaimHandleState = "abandoned"
    ClaimHandleStateReleased  ClaimHandleState = "released"
)

type ClaimHandleRow struct {
    // ... existing fields ...
    State ClaimHandleState  // NEW (replaces HeldDurable bool)
}

type ClaimHandleTable interface {
    // ... existing Get / ListByX methods stay ...

    // REMOVED: SetHeldDurable, ListHeldDurableByInstance.

    // ADDED:
    Promote(ctx, id, supervisorID, newState, tx) error
        // active → committed / abandoned (claimant-guarded)
    Release(ctx, id, supervisorID, tx) error
        // committed → released (claimant-guarded; supervisorID is the
        // supervisor that fired the producer Release, not the original
        // holder)
    ListByState(ctx, state, tx) ([]ClaimHandleRow, error)
    ListByInstanceAndState(ctx, instanceID, state, tx) ([]ClaimHandleRow, error)
}
```

#### Migration staging (5-stage playbook, run-tree pattern)

1. **Additive: introduce `state` + dual-write.** ALTER ADD COLUMN with `DEFAULT 'active'`; backfill `committed` from `held_durable = TRUE`; runtime dual-writes both columns; tests green.
2. **Reader cutover.** Every `HeldDurable` read flips to `State`; dual-write keeps `held_durable` as fallback.
3. **Promote-not-delete semantics.** `Delete` at terminal becomes `Promote(committed/abandoned)`. New `SweepClaimHandleRetention` parallels `SweepRunTreeRetention`. Scenario tests that asserted "row gone after terminal" flip to "row state = 'committed'/'abandoned' after terminal."
4. **Drop the bool.** ALTER DROP COLUMN; drop dual-write scaffold; drop old `idx_claim_handles_held_durable`; create new state-based indexes.
5. **Concept catalog + invariants + docs.** Update `concept:claim-handle` / `claim-lifetime` / `claim-tree` / `cancel-siblings` / `asset`; update `@blessed-invariant 22` text; update CLAUDE.md gotchas; update CHANGELOG.md.

#### What stays the same

Producer-side state machine (Open / Commit / Abandon / Release verbs). `rimsky_claim_holders` table + co-hold mechanic (already has its own `state` column). Asset URL identity form (`{template_node_alias}.{claim_alias}`). Auto-terminal aggregation logic (counters + recursive walker). Cancel-siblings + descendant-cancel logic — only the skip-filter expression changes (`if HeldDurable` → `if State != Active`).

#### Forensics gain (complementary to A+B)

The A+B follow-up added `rimsky_lineage.outcome` so Commit + Abandon + force_cancelled are all queryable from the lineage table. The state-column refactor adds a parallel surface on the claim_handle itself: `state = 'abandoned'` and `state = 'released'` rows are queryable directly without needing the lineage join. The two surfaces are complementary — lineage answers "what data versions resulted from this instance?" (content-identity-over-time), claim_handles answer "what claims existed in this instance and how did they resolve?" (claim-lifecycle-over-time).

---

### Item 2 — A+B review pass (small)

Single subagent reviewer dispatch against the uncommitted A+B follow-up (lineage `outcome` column + 7 event-emit sites + `claim_commit` → `claim_terminal` rename propagation through openlineage subscriber + control-api + CLI tests + CLAUDE.md). Standard `ok-planner:review-work` + `ok-planner:review-cleanup` loop if issues found. ~1 cycle of work; small but discipline-matching for the work that just landed.

**Goal:** clean reviewer pass on the new code before commit, given how much surface the A+B work touched (lineage shape change, new persistence column, new event-emit sites at sub-claim acquisition, fan-out dispatch, sub-graph dispatch, claim resolution, all with payload-redaction invariants).

---

### Item 3 — Smoke flake investigation (small)

Two flakes surfaced across the post-delivery cycles:

- **`TestParkedLifecycleResumeOnDeadline`** — cycle 8 claimed a fix (10s `resumeAt` budget + Success-script-swap-before-probes). Resurfaced in the A+B follow-up dispatch's `make test-all`. Either the fix didn't hold or there's a second race.
- **`TestPerInstanceOrderingInvariant_Concurrent`** — surfaced in the A+B follow-up under heavy parallel load (failed under a separate go test running in parallel; passes in isolation; passes under normal `make test-all`).

Approach:

1. Run each failing test under heavy concurrent load (50–100 sequential runs alongside a separate `make test-all`) to reproduce.
2. Diagnose root cause per failure: real race in production code (fix the code); fixture timing (fix the fixture); testcontainers parallelism artifact (serialize / scope container allocation).
3. Validate fix with 50–100 consecutive runs under heavy load.
4. Document root cause + fix in CHANGELOG.

Honest expectation: testcontainers parallelism artifacts may surface as the answer for one or both. If so, the fix is fixture-level (serialize the test) rather than production-code-level.

---

### Item 4 — Cold-read paydown (medium)

Four files exceed the `.claude/rules/cold-read-cheatsheet.md` 500-line guideline:

| File | Lines | Functions | Largest function |
|---|---|---|---|
| `runtime/runner_acquire.go` | 1096 | 18 | `tryAcquire` (~250 lines) |
| `runtime/terminal_decision.go` | 667 | 5 | `ResolveClaimHandleTerminal` (~190 lines) |
| `runtime/subgraph_dispatch.go` | 572 | (several) | — |
| `runtime/auto_terminal.go` | 553 | (several) | — |

Natural splits:

- **`runner_acquire.go`** → `runner_acquire_named_locks.go` (named-lock acquisition; `takeNamedAdvisoryLocks`, `acquireOneLock`, `acquireNamedLock`); `runner_acquire_claims.go` (claim acquisition; `acquireClaim`, `evaluateScopeConflict`); `runner_acquire_holders.go` (holder INSERTs; `insertHeldClaimHoldersAtAcquire`, `insertCoHolderClaimHoldersAtAcquire`, `findHoldingSubgraphForAcquirer`). Top-level `tryAcquire` stays as the orchestration entry but shrinks to dispatch into the per-concern files.
- **`terminal_decision.go::ResolveClaimHandleTerminal`** (~190 lines, 8 distinct operations) → orchestration shell + per-branch helpers. Sketch shape: `dispatchDataProcessingTerminal`, `fireProducerVerb`, `writeTerminalLineage`, `promoteOrDelete` (this is where the state-column refactor from item 1 would integrate cleanly), `bumpParentCounter`, `recurseSiblingCancel`, `recurseParentChain`. Each helper is bounded; the top-level function reads as the workflow.
- **`subgraph_dispatch.go`** + **`auto_terminal.go`** — similar but smaller; each could be split per its top-level concerns (e.g. `auto_terminal.go` could become `auto_terminal_check.go` + `auto_terminal_chain.go` + `auto_terminal_aggregator.go`).

This is a pure refactor: no behavior change, all annotations preserved, all tests pass identically. Done well, it cuts the cold-read tax substantially and makes Item 1's surface much easier to work on. Done poorly, it spreads load-bearing complexity across more files for no gain.

**Sequencing note:** Item 4 should likely follow Item 1, not precede it. Item 1 changes `terminal_decision.go`'s shape significantly (the `Delete` branch becomes `Promote(committed/abandoned)`; the recursive descent extends; the lifetime branching consolidates). Doing Item 4 on the post-Item-1 shape avoids re-splitting after a major content change.

---

### Item 5 — Multi-replica `sensor-cron` test fixture (small)

`sensors/sensor-cron/` implements `pg_try_advisory_lock(SENSOR_CRON_KEY)` per-watch advisory lock for multi-replica safety per J1's pre-resolved decision. The implementation is in place; the test fixture is single-replica.

Scope:

- Add `sensors/sensor-cron/multi_replica_test.go` (or extend the smoke fixture in `test/smoke/`).
- Boot two `sensor-cron` instances against the same Postgres + same watch set.
- Drive a fire window where both replicas attempt to acquire the advisory lock.
- Assert exactly one fires the cron action per window; the other backs off cleanly; no duplicate observations land at the control-api.
- Fail-injection: kill the holding replica mid-window; assert the other picks up at the next tick.

Small scope (~1 scenario test file + minor fixture extensions); covers the only documented multi-replica behavior in the bundled sensor reference impls.

---

### Item 6 — End-to-end coverage uniformity for asset/lineage scenarios (medium)

The cycle-3 reviewer noted that some scenarios pin shapes via unit/pure-helper tests where end-to-end pgtest coverage would be stronger. Cycles 1, 3, 4 substantially expanded end-to-end coverage (held-durable promotion, message cascade, fan-out aggregation, recursive parent resolution); the A+B follow-up added three more end-to-end scenarios (claim_abandon_lineage, force_cancelled_lineage, forensics/fanout_post_mortem). But coverage is not uniform — asset + lineage scenarios still mix shape-pinning with end-to-end.

Scope:

1. Audit `test/scenarios/asset/*` and `test/scenarios/lineage/*` — classify each test as shape-pinning vs end-to-end. Shape-pinning tests verify struct fields / SQL column round-trips without booting the harness; end-to-end tests boot Postgres + scheduler + supervisor + control-api + bundled producer/executor and drive a real cascade.
2. For each shape-pinning test, decide: keep as-is (unit-level coverage is appropriate for the property), upgrade to end-to-end (the load-bearing property needs the full stack), or split (keep the unit test + add an end-to-end companion).
3. Land the upgrades / additions. Validate with `make test-all`.

Some shape-pinning tests will stay: they're appropriate for property-pinning where the harness boot is overhead. The audit's job is to identify the load-bearing properties whose unit-level pinning hides integration drift.

Bounded: ~20 existing test files in scope; estimate 5–10 will need upgrade or companion tests.

## Recommended sequencing

Six steps, each justified. The reasoning matters as much as the order; brainstorm can re-order if a constraint changes.

### Step 1 — A+B review pass (Item 2)
**Do first.** The A+B work landed without a reviewer pass because the architectural-decisions discussion took priority. Cheap insurance before any commit: single reviewer dispatch + the standard `ok-planner:review-cleanup` loop if issues found. If clean, the A+B work is shippable; if not, the cleanup cycles tighten it. Either way, the commit decision in Step 2 lands on reviewed code.

**Why first:** the cycle-1-through-9 history showed reviewers consistently catch real issues (5 architectural gaps in cycle 8 alone). Skipping the review on A+B because "it was small" is the kind of false economy the cleanup loop discipline exists to prevent.

### Step 2 — Commit the uncommitted work
**Three staged commits.** Each is a logical milestone with a CHANGELOG narrative already written.

- **Commit 2a:** the 2026-05-15 plan delivery + the 9-cycle cleanup loop. ~316 files; +10260/-3703 lines. CHANGELOG carries the cycle-by-cycle history.
- **Commit 2b:** the A+B follow-up (lineage `outcome` column + 7 event-emit sites + the `claim_commit` → `claim_terminal` rename propagation). Distinct logical unit.
- **Commit 2c:** the post-review docs (the two new concept files, CLAUDE.md gotcha, the two sketches under `.ok-planner/sketches/`). Lightweight; lands the architecture-discussion outputs.

**Why staged not single:** each commit is independently revertible; each has a coherent CHANGELOG paragraph; future history-traversers see the logical phases of the delivery cleanly. Single big commit would lose that grain.

**Why commit before Items 3+:** every subsequent item modifies code; without committing first, history conflates the original delivery with the post-delivery work.

### Step 3 — Smoke flake investigation (Item 3)
**Small focused dispatch.** Either a real race (fix the code) or a fixture issue (fix the fixture). Don't let it linger — the CI hit-rate matters more than the perfect diagnosis.

**Why here:** small enough to land before the big refactor; getting it off the "known flakes" list reduces noise during Item 1's intensive testing.

### Step 4 — State-column refactor (Item 1)
**The big one. Its own brainstorm → spec → plan → execute-plan cycle.** Estimated 5+ cycle-equivalents per the run-tree cutover playbook (cycles 8–10 closed the 5 stages for `rimsky_node_runs`; this refactor is smaller surface but the discipline matches).

**Why here:** waiting until Items 1–3 settle means Item 1 starts with a stable foundation (reviewed, committed, flake-free). Doing Item 1 first risks the refactor landing on uncommitted code with a known flake masking real test failures.

**Why before Item 4:** Item 4 reshapes the files Item 1 modifies (`terminal_decision.go` especially). Doing Item 1 first means Item 4's restructure operates on the post-refactor content; doing Item 4 first means re-splitting after Item 1 lands.

### Step 5 — Cold-read paydown (Item 4)
**After Item 1's state column is in.** Pure refactor; no behavior change; runs as its own plan. The natural splits in Items 1's post-refactor `terminal_decision.go` will be cleaner than the current shape.

**Why not earlier:** see Step 4's rationale. Doing this before Item 1 is wasted work.

**Why a separate plan, not folded into Item 1:** Item 1 is a behavior-preserving model change with schema impact; Item 4 is a pure code-organization refactor with no schema impact. Folding them dilutes both. Separate plans, separate review cycles.

### Step 6 — Test coverage hardening (Items 5 + 6)
**Parallel or sequential; no dependencies between them.** Items 5 (multi-replica `sensor-cron`) and 6 (asset/lineage end-to-end coverage uniformity) can be planned together in a single spec or split. The brainstorm decides.

**Why last:** smallest individual items; benefit from the prior items landing first (Item 4's refactor reduces cold-read tax on the coverage audit; Item 1's state column adds new test surface that Item 6's audit picks up).

### Alternative orderings

- **If shipping Item 1 sooner matters more than cold-read tax**: 1 → 2 → 3 → 4 → 5 + 6, accepting that Item 1 lands on unreviewed/uncommitted A+B. Higher risk.
- **If the cold-read tax bites immediately** (a contributor reports it's slowing them down): pull Item 4 forward to immediately after Item 2; accept that Item 1 will re-shape some of the split files.
- **If the smoke flakes are blocking CI**: pull Item 3 forward to before Item 2. The reviewer for Item 2 needs a clean `make test-all` to verify; a noisy CI would obscure real issues.

## Open questions

- **Commit strategy.** Single big commit covering the entire 2026-05-15 delivery + cleanup + A+B + docs? Or staged commits (delivery / cleanup / A+B / docs as four separate commits)? Latter is more readable in history but means four commit-message reference passes against the CHANGELOG.
- **One brainstorm or multiple?** Six items vary in size from "small dispatch" (Items 2, 3, 5) to "large refactor with 5-stage cutover" (Item 1). Brainstorm convention is one feature per spec. Likely the right shape is: one brainstorm session that scopes all six into THREE specs: (a) the state-column refactor (Item 1); (b) the cold-read paydown (Item 4, conditional on (a)); (c) the cleanup grab-bag (Items 2, 3, 5, 6 — each small, but cohesive as "post-delivery sharpening"). The brainstorm session itself can decide the split.
- **Trace context columns from the 2026-05-16 traceability sketch.** That sketch proposes adding `trace_id` / `span_id` to `rimsky_claim_handles` (among other tables). If both refactors land, they should coordinate the schema change (one migration adds state AND trace context) to avoid two churns. If the traceability work is v1.x and this milestone is pre-v1, they're temporally separate — but worth deciding.
- **Item 1's retention defaults.** `retention.claim_handles_trailing = 30d` matches the lineage default, but the right number depends on operator practice. Brainstorm-time decision.
- **Item 1's `released` state — necessary?** YAGNI candidate. Three reads articulated in the original sketch; brainstorm picks one.
- **Item 4's split granularity.** Splitting `runner_acquire.go` into three files vs five vs the exact natural concerns. Brainstorm should walk through the proposed splits and confirm the boundaries.
- **Item 6's audit deliverable.** Does the brainstorm produce the audit, or does the brainstorm scope the audit as a pre-work step that a focused dispatch does, with the results feeding the spec?

## Risks / unknowns

- **Brainstorm overload.** Six items in one session might be too much. The "one feature per spec" convention exists for a reason — bundling them risks producing a spec that's too coarse to execute well. Mitigation: brainstorm starts by deciding the spec split (the question above) before getting into the content of any one item.
- **Item 1 is the schedule risk.** The state-column refactor's 5-stage cutover took 5+ cleanup cycles for the run-tree equivalent. Claim-handles is smaller surface but the same discipline applies. Estimating the total cycle count for the milestone, Item 1 dominates.
- **Item 4 done before Item 1 wastes work.** Splitting `terminal_decision.go` before Item 1 changes its shape means re-splitting after. The sequencing note above addresses this.
- **The A+B work isn't reviewed yet.** Item 2 is small, but if the reviewer surfaces meaningful issues (the cycle-1-through-9 pattern), it could turn into multiple cycles. Mitigation: do Item 2 first; if it converges quickly, great; if not, plan accordingly.
- **Smoke flakes may not be in our code.** If both flakes turn out to be testcontainers parallelism artifacts (Postgres container start-time variance under load), the fix is "serialize the affected scenarios" or "extend timeouts" rather than a production-code fix. That's still resolution but it's flake-mitigation not bug-fix.
- **Cold-read paydown is invisible work.** No new behavior; the diff is "this function moved to that file." Reviewers may not see the value; future contributors will.
- **Multi-replica `sensor-cron` scenario needs careful fixture design.** Two replicas + Postgres + advisory locks + fire windows = a test that can itself become flaky if not bounded carefully.
- **Item 6's audit may surface more work than the audit's bounded scope suggests.** "Some asset / lineage tests should be upgraded to end-to-end" could become "actually 15 tests need restructuring, and along the way I noticed the harness boot path has its own coverage gaps." Audits expand.

## What this is not

- **Not a v1 commitment.** Pre-v1 paydown + sharpening. Doesn't decide v1's data model, observability surface, or operator surface.
- **Not a new feature delivery.** No new protocol surfaces, no new bundled services, no new control-api endpoints. Items 1 and 4 are refactors; Items 2, 3, 5, 6 are discipline / coverage.
- **Not a re-do of the held-durable lifecycle behavior** (Item 1). Behavior stays the same; modeling changes.
- **Not consolidated with the 2026-05-16 traceability sketch.** That sketch is a v1.x feature, much larger scope. This milestone is pre-v1 cleanup that happens to share one schema-table-of-interest with traceability; coordination at migration time is captured as an open question.
- **Not a commit of any of the uncommitted work.** The commit decision is the user's; this sketch only flags it as an open question in sequencing.

## What the next session should know

The brainstorm session that scopes this milestone is a fresh context. Things that aren't in the codebase but informed this sketch:

### User preferences from the prior session

- **The user prefers larger planning milestones over many small ones.** This sketch is an omnibus by user request. The brainstorm should probably preserve the milestone framing — produce a small number of cohesive specs rather than six tiny ones.
- **Recommended spec split (from the Open Questions section, restated):** three specs. (a) State-column refactor (Item 1) — its own spec because the surface is large and the discipline is heavy. (b) Cold-read paydown (Item 4) — its own spec because it's a distinct concern with a sequencing constraint. (c) Cleanup grab-bag (Items 2, 3, 5, 6) — one spec covering the four small items as a cohesive "post-delivery sharpening" deliverable. Brainstorm can revise; this is the natural starting point.
- **"Option C" was the user's explicit choice for the held-durable refactor** — state column on the row, parallel to the run-row pattern. Option D (separate `rimsky_assets` table) was articulated and rejected. The brainstorm doesn't need to re-litigate the table-split alternative.
- **Park (not Snooze) for proto naming** stays. Documented in the archived plan notes; the user confirmed this in the inline review.
- **Multi-supervisor scope of `cancel_siblings` is intentional**, documented in `concept:cancel-siblings.md` + CLAUDE.md gotchas. Don't propose cross-supervisor cancellation without explicit user direction.

### State of the working tree

- **Branch `main`, no commits.** Everything below is uncommitted in the working tree.
- **316 changed files, +10260/-3703 lines** from the 2026-05-15 plan delivery + 9-cycle cleanup loop.
- **A+B follow-up on top of that** (lineage `outcome` column + 7 event-emit sites + rename propagation). Uncommitted, unreviewed.
- **Two new concept files** (`.ok-planner/design/concepts/claim-tree.md` + `cancel-siblings.md`), CLAUDE.md gotcha update, `claim-co-holdership.md` cross-reference update, concepts.md TOC update. Uncommitted.
- **Two sketches** under `.ok-planner/sketches/`: the 2026-05-16 traceability sketch and this one. Uncommitted.
- **`make build-all && make lint && make test-all` clean** as of the end of the A+B follow-up (modulo the two known flakes Item 3 investigates).

### Archived context the next session should consult

- **`.ok-planner/history/plans/2026-05-15-data-platform-extensions-plan.md`** — the original plan. Section H was CUT (deliberately, not deferred); don't propose reviving it.
- **`.ok-planner/history/plans/2026-05-15-data-platform-extensions-plan-notes.md`** — the dispatch + cleanup-cycle history. Critical context for Item 1: the run-row-lifecycle cutover staging plan + lessons learned are in cycles 8–10's entries. Read those before scoping Item 1's migration staging.
- **`.ok-planner/history/specs/2026-05-15-data-platform-extensions-design.md`** — the original spec. Held-durable lifecycle described there; the refactor preserves the behavior; the spec doesn't need rewriting.
- **`.ok-planner/sketches/2026-05-16-full-traceability-sketch.md`** — the traceability sketch. Item 1's schema migration should coordinate with this if both land near each other (open question in the sketch).

### Design log to consult

`.ok-planner/design/concepts/` has the canonical noun catalog. For Item 1 specifically, read in full:

- `claim-handle.md` — the row Item 1 reshapes.
- `claim-lifetime.md` — the `subgraph` vs `durable` distinction Item 1 makes structural.
- `claim-tree.md` — the recursive resolution mechanism that interacts with state.
- `cancel-siblings.md` — the skip-filter sites that Item 1 changes the expression of.
- `claim-co-holdership.md` — the co-hold mechanic Item 1 preserves.
- `auto-terminal.md` — the resolution path Item 1 reshapes.
- `asset.md` — the surface Item 1 makes structural (asset = `state = 'committed' AND lifetime = 'durable'`).

For Items 2–6, the relevant concepts are narrower; consult per item.

### Things to ask the user explicitly in brainstorm

The Open Questions section above lists ~8 items; these are the ones most worth surfacing as explicit brainstorm-dialogue questions rather than agent assumptions:

1. **Commit strategy** (staged vs single).
2. **Spec split** (one spec / two specs / three specs).
3. **Trace context coordination** with the 2026-05-16 traceability sketch — should Item 1's migration include `trace_id` / `span_id` columns proactively, or wait until traceability's own spec lands?
4. **`released` state — keep or drop?** YAGNI candidate; needs an explicit opinion.
5. **Retention defaults** — `committed` rows kept indefinitely (they're the asset surface) but `abandoned` / `released` swept on what window?
6. **Item 4's split granularity** — three splits per file vs more vs fewer.
7. **Item 6's audit deliverable** — brainstorm produces it, or pre-work dispatch produces it for the spec to consume?

### Brainstorm posture suggestion

- **Don't re-litigate settled decisions.** Section H cut, Park naming, multi-supervisor scope, Option C choice, A+B work itself — all settled. Brainstorm refines the spec scope, doesn't re-open architecture.
- **Watch for scope creep.** Six items is already at the edge of "one brainstorm session." Adding "while we're at it, let's also..." items pushes past the edge. New items found during brainstorm should become their own sketches or get explicitly deferred.
- **Item 1 dominates the schedule.** The other five are small. If brainstorm runs long, prioritize getting Item 1's spec right; the cleanup grab-bag can land on a simpler spec.
- **The run-row-lifecycle playbook is the precedent.** Item 1's 5-stage migration shape is taken directly from cycles 8–10's history. The brainstorm should reference that history when scoping rather than re-deriving the staging from first principles.
