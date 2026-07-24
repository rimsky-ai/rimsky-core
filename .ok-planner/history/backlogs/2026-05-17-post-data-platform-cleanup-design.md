# Post-Data-Platform Cleanup Milestone — Design

**Date:** 2026-05-17
**Source sketch:** `.ok-planner/sketches/2026-05-17-post-data-platform-cleanup-milestone-sketch.md`
**Status:** Spec (awaiting plan)

## Overview

A milestone covering six post-delivery follow-ups after the 2026-05-15 data platform extensions plan + the 9-cycle cleanup loop + the A+B forensics follow-up. The items range from a sizable schema refactor (Item 1) to small dispatches (Items 2, 3, 5) and a bounded audit (Item 6). They are grouped here as a single spec for planning convenience; the items are not coupled at the data-flow level.

### Item index

1. **Item 1** — Claim-handle state-column refactor. Replace `held_durable bool` on `rimsky_claim_handles` with an explicit `state` enum (`active`, `committed`, `abandoned`). Collapses the held-durable guard network into a single discriminator; makes the durable-claim contract structural rather than discipline-based.
2. **Item 2** — A+B follow-up review pass. Single reviewer dispatch against the uncommitted A+B follow-up (`col:rimsky_lineage.outcome` + 7 event-emit sites + `claim_commit` → `claim_terminal` rename propagation).
3. **Item 3** — Smoke flake investigation. Two known flakes (`TestParkedLifecycleResumeOnDeadline`, `TestPerInstanceOrderingInvariant_Concurrent`) reproduced under load, classified, fixed.
4. **Item 4** — Cold-read paydown. Split two oversized runtime files concretely; re-measure two borderline files after Item 1 lands and split if still over guideline.
5. **Item 5** — Multi-replica `sensor-cron` test fixture. Two-replica advisory-lock contention scenario.
6. **Item 6** — Asset/lineage end-to-end coverage uniformity. Classification-then-upgrade pass over `test/scenarios/asset/*` and `test/scenarios/lineage/*`.

### Execution sequencing

The items are sequenced — not coupled, but ordered for low-risk delivery:

1. **Item 2** first. The A+B follow-up is unreviewed; clean reviewer pass before any commit decision.
2. **Item 3** next. Get known flakes off the board before Item 1's heavy test discipline starts (clean CI baseline matters when stress-testing a refactor).
3. **Item 1** — the big one. Its own 5-stage cutover discipline; biggest design surface; reshapes files Item 4 will later split.
4. **Item 4** after Item 1. Item 1 changes `code:runtime/terminal_decision.go::ResolveClaimHandleTerminal` significantly; splitting it before Item 1 means re-splitting after.
5. **Items 5 + 6** last. Smallest; no dependencies on Items 1–4 except that Item 6's audit benefits from running against the post-Items-1+4 codebase.

Within the spec, the design content is presented in execution order.

### Out of scope

- **Commit strategy.** The spec sequences work and identifies natural quiescence points, but does not prescribe commit grain (single commit vs staged). Commits happen at execute-plan time, after work and review, per project discipline (`file:.claude/rules/rules.md`).
- **v1 commitments.** Pre-v1 paydown; doesn't decide v1's data model, observability surface, or operator surface.
- **New feature delivery.** No new protocol surfaces, no new bundled services, no new control-api endpoints. Items 1 and 4 are refactors; Items 2, 3, 5, 6 are discipline / coverage.
- **Held-durable lifecycle behavior change.** Item 1 changes the modeling, not the behavior. Held-durable claims still persist past holding-subgraph completion, still release only on explicit operator action or instance termination, still skip the orphan reaper.
- **Trace-context coordination with the 2026-05-16 traceability sketch.** Item 1's migration adds `state` only; trace-context columns are the traceability sketch's concern, on its own schedule. Pre-v1 freedom to break authorizes two migrations on the same table over time.

---

## Item 2 — A+B follow-up review pass

### Scope

Uncommitted work landing this review: the lineage `outcome` column + 7 event-emit sites + the `claim_commit` → `claim_terminal` rename propagation through `subscribers/openlineage/`, `code:control/controlapi/`, the CLI tests, and the `file:CLAUDE.md` gotcha block.

### Process

Standard `ok-planner:review-work` dispatch over the uncommitted working tree, then `ok-planner:review-cleanup` loop if issues found. The "fix every issue" rule applies; the reviewer's severity labels are not a triage axis.

### Done

Reviewer reports clean and the working tree is in a coherent state for the Item 3 dispatch to follow.

---

## Item 3 — Smoke flake investigation

### Known flakes

- `TestParkedLifecycleResumeOnDeadline` — cycle 8 claimed a fix (10s `resumeAt` budget + Success-script-swap-before-probes). Re-surfaced in the A+B follow-up dispatch's `cmd:make test-all`.
- `TestPerInstanceOrderingInvariant_Concurrent` — surfaced under heavy parallel load (failed when a separate `cmd:go test` ran in parallel; passes in isolation and under normal `cmd:make test-all`).

### Investigation process

For each flake:

1. **Reproduce under load.** 50–100 sequential runs alongside a separate `cmd:make test-all` running in parallel. Reproduction rate informs classification.
2. **Classify root cause.** Three categories:
   - **Real race in production code** — fix the code; verify under load.
   - **Fixture timing** — fix the fixture; verify under load.
   - **`testcontainers` parallelism artifact** — Postgres container start-time variance under load. Fix is fixture-level (serialize the affected scenario or extend timeouts), not production-level.
3. **Validate fix.** 50–100 consecutive runs under load, clean.
4. **Document.** Root cause + fix recorded in `file:CHANGELOG.md` Unreleased section.

### Done

Both flakes off the known-flake list; documented root cause + fix in `file:CHANGELOG.md`. If the diagnosis is "testcontainers parallelism artifact" rather than a production race, that's still a valid resolution.

---

## Item 1 — Claim-handle state-column refactor

The largest item. Replaces `held_durable bool` with a `state TEXT` column on `table:rimsky_claim_handles`, collapses the ~10 `HeldDurable` consultation sites into single-discriminator dispatch, and makes the durable-claim contract structural. Parallel in shape to the run-tree cutover that landed in cycles 8–10 for `table:rimsky_node_runs`.

### Schema

New column on `table:rimsky_claim_handles`:

```sql
ALTER TABLE rimsky_claim_handles
    ADD COLUMN state TEXT NOT NULL DEFAULT 'active'
    CHECK (state IN ('active', 'committed', 'abandoned'));

-- After Go-side cutover (Stage 4):
ALTER TABLE rimsky_claim_handles DROP COLUMN held_durable;
```

Three states:

| state | meaning | `holder_supervisor_id` | row deleted by |
|---|---|---|---|
| `active` | currently held by a supervisor, heartbeating | NOT NULL | `Delete` at terminal (legacy path; flipped to `Promote` in Stage 3) |
| `committed` | producer `Commit` fired; row preserved past terminal | NULL | `SweepClaimHandleRetention` for `lifetime: subgraph`; `Delete` on producer `Release` for `lifetime: durable` |
| `abandoned` | producer `Abandon` fired (natural or force-cancel) | NULL | `SweepClaimHandleRetention` |

#### CHECK constraints

```sql
-- Active rows must have a holder; non-active rows must not.
CHECK (state != 'active' OR holder_supervisor_id IS NOT NULL);
CHECK (state = 'active' OR holder_supervisor_id IS NULL);
```

The second CHECK is the explicit semantic shift: on `Promote(active → committed/abandoned)`, `holder_supervisor_id` is nulled. Mirrors the `col:rimsky_node_runs.claimed_by` nulling on terminal. Audit data ("which supervisor terminated this claim?") lives in `table:rimsky_lineage` (already carries this post-A+B), not on the row.

#### Partial indexes

Today's `idx_claim_handles_held_durable` is dropped in Stage 4. The state column gains supporting indexes; the exact DDL follows the column names that exist on `table:rimsky_claim_handles` today (`holder_supervisor_id`, `holder_node_id`, `node_run_id`, `scope_data JSONB`, etc.):

```sql
CREATE INDEX rimsky_claim_handles_active_idx
    ON rimsky_claim_handles (holder_supervisor_id) WHERE state = 'active';
CREATE INDEX rimsky_claim_handles_committed_durable_idx
    ON rimsky_claim_handles (holder_node_id) WHERE state = 'committed' AND lifetime = 'durable';
```

The committed-durable index supports the asset query (`WHERE state = 'committed' AND lifetime = 'durable' AND holder_node_id IN (…)` joining through `table:rimsky_nodes.id` to filter by `instance_id`).

The existing conflict-predicate index (today scoped to live rows via `expires_at`) must be updated so its `WHERE` clause covers both live (`state = 'active'`) and successfully-promoted (`state = 'committed'`) rows — `committed` rows still conflict for `concept:scope`-byte-equal acquisition until they're swept or Released. The lead-column shape follows the existing conflict-query plan (producer + `scope_data` byte-equality); whether the comparison uses raw JSONB or a derived expression is a Stage-3 implementation choice based on existing query plans, not a design decision.

### State transitions

| Transition | Site | Trigger |
|---|---|---|
| `(none) → active` | `code:runtime/runner_acquire.go::tryAcquire` (INSERT) | acquisition tx |
| `active → committed` | `code:runtime/terminal_decision.go::ResolveClaimHandleTerminal` (Commit branch) | producer `Commit` fired |
| `active → abandoned` | `code:runtime/terminal_decision.go::ResolveClaimHandleTerminal` (Abandon branch) | producer `Abandon` fired (natural or force-cancel) |
| `committed/abandoned → (deleted)` | `SweepClaimHandleRetention` (new sweep) | retention window expired |
| `committed → (deleted)` | `code:runtime/instance_termination.go::ReleaseHeldDurableClaims` OR `code:control/controlapi/assets.go` `DELETE` handler | producer `Release` fired (instance termination or operator `DELETE /instances/{id}/assets/{alias}`) |

The Release path (`committed → deleted`) goes through the existing `Delete` method, not a Promote variant — the locked-in 3-state model has no `released` state, so post-producer-`Release` the row is deleted outright.

#### Illegal transitions

- `committed → *` and `abandoned → *` (revival or verdict change).

Enforced at the Go layer via affected-rows check on the `Promote` query (predicate `WHERE state = 'active'`). Returns `ErrIllegalClaimHandleTransition` (mirror of `code:foundation/cascade.ErrIllegalTransition`) on affected-rows = 0.

Reclaim of an already-held row at INSERT time (the `(none) → active` path attempting to overwrite an existing `active` row) is gated by the scope-conflict predicate at `code:runtime/runner_acquire.go::tryAcquire`, not by a state-column check — that's the existing acquisition discipline and is not affected by this refactor.

### Go-side surface

```go
// foundation/spec/claim_handle_state.go (new)
type ClaimHandleState string

const (
    ClaimHandleStateActive    ClaimHandleState = "active"
    ClaimHandleStateCommitted ClaimHandleState = "committed"
    ClaimHandleStateAbandoned ClaimHandleState = "abandoned"
)

// foundation/persistence/claim_handles.go
type ClaimHandleRow struct {
    // existing fields…
    State ClaimHandleState // NEW — replaces HeldDurable bool
    // HeldDurable removed in Stage 4
}

type ClaimHandleTable interface {
    // Existing Get / ListByProducerScope / ExtendHeartbeat / DeleteIfExpired / etc. stay.

    // REMOVED in Stage 4:
    //   SetHeldDurable
    //   ListHeldDurableByInstance

    // ADDED:
    Promote(ctx context.Context, id ClaimHandleID, supervisorID SupervisorID,
        newState ClaimHandleState, tx Tx) error
    // active → committed | abandoned. Claimant-guarded:
    //   WHERE id = $1 AND state = 'active' AND holder_supervisor_id = $2
    // Sets state = $newState, holder_supervisor_id = NULL, resolved_at = now()
    // in the same UPDATE. The retention sweep filters on resolved_at.
    // Returns ErrIllegalClaimHandleTransition on affected-rows = 0.

    ListByState(ctx context.Context, state ClaimHandleState, tx Tx) ([]ClaimHandleRow, error)
    ListByInstanceAndState(ctx context.Context, instanceID InstanceID,
        state ClaimHandleState, lifetime ClaimLifetime, tx Tx) ([]ClaimHandleRow, error)
    // Used by the asset query: ListByInstanceAndState(instance, committed, durable).

    // Delete stays. Used by:
    //   - SweepClaimHandleRetention (retention window expired; state IN ('committed','abandoned'))
    //   - ReleaseHeldDurableClaims (instance termination, post-producer-Release)
    //   - assets.go DELETE handler (operator release, post-producer-Release)
    // The Release-path Delete is NOT claimant-guarded against the original
    // acquirer — by then the row's holder_supervisor_id is NULL (per Promote
    // above), and the supervisor invoking ReleaseHeldDurableClaims may not
    // be the original acquirer anyway. See @blessed-invariant 4 update in
    // Stage 5.
}
```

### Asset / durable behavior (unchanged semantics, new modeling)

The durable-claim contract is preserved end-to-end:

- A `lifetime: durable` claim still persists past its holding-subgraph's terminal. The modeling change: instead of "row stays + `held_durable = TRUE`", it's "row stays + `state = 'committed'`".
- Release is still explicit: `code:runtime/instance_termination.go::ReleaseHeldDurableClaims` (on instance termination) or the `code:control/controlapi/assets.go` `DELETE /instances/{id}/assets/{alias}` handler (operator-driven). Both fire producer `Release`, then `Delete` the row claimant-guarded.
- The asset query becomes: `WHERE state = 'committed' AND lifetime = 'durable' AND instance_id = ?`. Replaces `WHERE held_durable = TRUE AND n.instance_id = ?`.
- The orphan-claim reaper's skip rule becomes: `WHERE state = 'active' AND expires_at < now()` (no longer needs the `held_durable = FALSE` clause; the state filter handles it implicitly because `committed`/`abandoned` rows aren't in the candidate set).

### Cancel-siblings + auto-terminal skip-filter changes

The recursive descent in `code:runtime/terminal_decision.go::cancelDescendantClaims` + `code:runtime/auto_terminal.go::resolveParentClaimChain` reads its skip-filter expression as `if HeldDurable continue`. Post-refactor: `if state != ClaimHandleStateActive continue`. Functionally equivalent under the durable-Commit contract (a row that's been promoted to `committed` or `abandoned` is not a candidate for force-cancellation), but expression-level uniform with the rest of the state-column reads.

`concept:cancel-siblings` documents this skip explicitly (along with the multi-supervisor scope filter and the held-durable preservation rule); the concept-doc Notes section gets an entry.

### Retention sweep

A new sweep, `SweepClaimHandleRetention`, modeled on the existing time-based `SweepLineageRetention` pattern in `code:runtime/retention_sweeps.go`:

```go
// runtime/sweep_claim_handle_retention.go (new)
func SweepClaimHandleRetention(ctx context.Context, db persistence.Database,
    cutoff time.Duration) (deleted int64, err error)
// Deletes claim_handle rows where:
//   state IN ('committed', 'abandoned')
//   AND (state = 'abandoned' OR lifetime = 'subgraph')  -- never sweep durable-Commit rows
//   AND (resolved_at + cutoff) < now()
//   AND holder_supervisor_id IS NULL  -- defense-in-depth; state-filter already implies this
// Serialization: runs under the scheduler-tick advisory lock; no per-row
// claimant-guard column to check (the rows being swept have no holder).
```

A new `col:rimsky_claim_handles.resolved_at TIMESTAMPTZ NULL` column is added in Stage 1: set to `now()` by the `Promote` query; the retention sweep filters on `resolved_at + retention.claim_handles_trailing < now()`.

Configuration extends the existing `code:runtime/retention_sweeps.go::RetentionConfig` struct:

```yaml
retention:
  lineage_trailing: 30d           # existing time-based key (precedent)
  recent_frames_kept: <N>         # existing count-based key (unrelated to this sweep)
  claim_handles_trailing: 30d     # NEW; time-based, mirrors lineage_trailing
```

The sweep runs on the same scheduler-tick cadence as the existing retention sweeps; the scheduler-tick advisory lock (`pg_try_advisory_lock(SCHEDULER_TICK_KEY)`) serializes across replicas — sweeps are not their own concurrent process.

### SQL transformation map

Every `held_durable` filter has a state-column equivalent. Same shape in `code:foundation/persistence/postgres/` and `code:foundation/persistence/sqlite/`:

| Site | Today | Post-refactor |
|---|---|---|
| `ListByProducerScope` | `WHERE (expires_at > now() OR held_durable = TRUE)` | `WHERE state IN ('active','committed')` |
| `ExtendHeartbeat` | `WHERE held_durable = FALSE` | `WHERE state = 'active'` (composite with claimant guard) |
| `DeleteIfExpired` (orphan reaper) | `WHERE held_durable = FALSE AND expires_at < now()` | `WHERE state = 'active' AND expires_at < now()` |
| `ListHeldDurableByInstance` | `WHERE held_durable = TRUE AND n.instance_id = $1` | (renamed) `ListByInstanceAndState`: `WHERE state = 'committed' AND lifetime = 'durable' AND n.instance_id = $1` |
| `SetHeldDurable(id, true)` | `UPDATE … SET held_durable = TRUE WHERE id = $1` | (renamed) `Promote(id, committed)`: `UPDATE … SET state = 'committed', holder_supervisor_id = NULL, resolved_at = now() WHERE id = $1 AND state = 'active' AND holder_supervisor_id = $2` |
| `Delete` (terminal natural delete in `ResolveClaimHandleTerminal`) | `DELETE FROM rimsky_claim_handles WHERE id = $1 AND holder_supervisor_id = $2` | `Promote(id, committed)` or `Promote(id, abandoned)` per branch |
| Release-path row discovery (`ReleaseHeldDurableClaims` + assets `DELETE`) | `WHERE held_durable = TRUE AND n.instance_id = $1` | `WHERE state = 'committed' AND lifetime = 'durable' AND n.instance_id = $1` |
| `Delete` (Release path; post-producer-`Release`) | `DELETE FROM rimsky_claim_handles WHERE id = $1 AND holder_supervisor_id = $2` (today claimant-guarded against the original acquirer) | `DELETE FROM rimsky_claim_handles WHERE id = $1` (claimant-guard drops because the row's `holder_supervisor_id IS NULL` post-Promote; the supervisor invoking Release may not be the original acquirer anyway) |
| `Delete` (retention sweep) | (new) | `DELETE FROM rimsky_claim_handles WHERE state IN ('committed','abandoned') AND resolved_at + $cutoff < now() AND holder_supervisor_id IS NULL` |
| Recursive walk `if HeldDurable` skip | bool check | `if state != ClaimHandleStateActive continue` |

### 5-stage migration playbook

Mirrors the run-tree lifecycle cutover (cycles 8–10). Each stage lands as a working-tree quiescence point with `cmd:make test-all` clean.

**Stage 1 — Additive: introduce `state` + `resolved_at`; dual-write.**

- Migration: `ALTER TABLE rimsky_claim_handles ADD COLUMN state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','committed','abandoned'))`.
- Migration: `ALTER TABLE rimsky_claim_handles ADD COLUMN resolved_at TIMESTAMPTZ NULL`.
- Migration: backfill in this order (all within the same migration transaction):
  1. `UPDATE rimsky_claim_handles SET state = 'committed', resolved_at = now(), holder_supervisor_id = NULL WHERE held_durable = TRUE` — note the `holder_supervisor_id = NULL` is part of the same row update; today's held-durable rows still carry their original acquirer's `holder_supervisor_id` (`SetHeldDurable` does not null it), so nulling it is required to satisfy the second CHECK constraint added in step 3.
  2. (Rows where `held_durable = FALSE` keep `state = 'active'` via the column default; no explicit UPDATE needed.)
  3. Add the holder-state-consistency CHECKs as two separate constraints (matching the Schema section form):
     ```sql
     ALTER TABLE rimsky_claim_handles
         ADD CONSTRAINT rimsky_claim_handles_active_has_holder
         CHECK (state != 'active' OR holder_supervisor_id IS NOT NULL);
     ALTER TABLE rimsky_claim_handles
         ADD CONSTRAINT rimsky_claim_handles_inactive_has_no_holder
         CHECK (state = 'active' OR holder_supervisor_id IS NULL);
     ```
- Go-side: `SetHeldDurable` writes BOTH `held_durable = TRUE` AND `state = 'committed'`, AND sets `holder_supervisor_id = NULL` AND sets `resolved_at = now()` — all four updates in the same UPDATE statement, matching the post-cutover `Promote` semantics. `Delete` at terminal stays as today (no row preservation yet).
- Tests green; no behavior change beyond the holder-id nulling on durable-Commit (which today is already semantically dead — `held_durable = TRUE` rows are skipped by the orphan reaper and by heartbeat-extending updates, so the `holder_supervisor_id` field on them is unused after the SetHeldDurable call). All columns populated consistently.

**Stage 2 — Reader cutover.**

- Every read site that consulted `held_durable` flips to consult `state`. The dual-write from Stage 1 keeps `held_durable` populated as a fallback.
- New `ListByInstanceAndState` accessor introduced; callers of `ListHeldDurableByInstance` flip over. `ListHeldDurableByInstance` becomes a thin wrapper, deprecated.
- Recursive walks (`cancelDescendantClaims`, `resolveParentClaimChain`) flip their skip-filter expression.
- Tests green; no behavior change.

**Stage 3 — Promote-not-delete semantics.**

- `Delete` at terminal in `ResolveClaimHandleTerminal` becomes `Promote(committed | abandoned)`. Row no longer disappears at terminal.
- New `SweepClaimHandleRetention` added, wired into the scheduler tick alongside `SweepRunTreeRetention`. `cfg:retention.claim_handles_trailing` default `30d`.
- Scenario tests that asserted "row gone after terminal" flip to "row state = 'committed'/'abandoned' after terminal; row gone after retention sweep advances clock past cutoff."
- `code:runtime/instance_termination.go::ReleaseHeldDurableClaims` flips its row-discovery query from `held_durable = TRUE` to `state = 'committed' AND lifetime = 'durable'`. The Release-then-Delete flow stays.
- `code:control/controlapi/assets.go` `DELETE` handler likewise flips its row-discovery query.
- Tests green; behavior change is row-preservation past terminal (visible in tests + lineage joins).

**Stage 4 — Drop the bool.**

- Migration: `ALTER TABLE rimsky_claim_handles DROP COLUMN held_durable`.
- Migration: drop `idx_claim_handles_held_durable`; create new state-based indexes (`_active_idx`, `_committed_durable_idx`, `_visible_for_scope_idx`).
- Go-side: drop `HeldDurable` field from `ClaimHandleRow`; drop `SetHeldDurable` and `ListHeldDurableByInstance` methods; drop dual-write scaffold.
- Tests green; no behavior change beyond the column drop.

**Stage 5 — Concept catalog + invariants + docs.**

- Update `.ok-planner/design/concepts/claim-handle.md`: replace `held_durable` references with `state`; document the 3-state model + transitions; document `resolved_at` + retention sweep; update Held-variant subsection accordingly.
- Update `.ok-planner/design/concepts/claim-lifetime.md`: change "auto-terminal Commit on `lifetime: durable` flips `held_durable = true` instead of deleting" to "auto-terminal Commit on `lifetime: durable` promotes to `state = 'committed'`; Release deletes the row outright"; refresh annotation sites.
- Update `.ok-planner/design/concepts/asset.md`: asset query becomes `state = 'committed' AND lifetime = 'durable'`; update annotation sites.
- Update `.ok-planner/design/concepts/claim-tree.md`: skip-filter expression change in the held-durable child invariant.
- Update `.ok-planner/design/concepts/cancel-siblings.md`: skip-filter expression change in the cancel-walker invariant.
- Update `.ok-planner/design/concepts/auto-terminal.md`: row deletion now goes through `Promote`-then-retention-sweep, not `Delete` at terminal; carve-out paths (`abandonOpenedClaim`) still use `Delete` directly (those rows never went through `Promote`).
- Update `.ok-planner/design/concepts/orphan-reaper.md`: skip-rule for held-durable replaced by `state = 'active'` filter; abandon-side path unchanged.
- Update `file:CLAUDE.md` gotchas: "Held-durable claim handles persist past holding-subgraph completion" — refresh text to refer to `state = 'committed'` + `lifetime = 'durable'` instead of `held_durable = TRUE`.
- Update `@blessed-invariant 4` text in `file:CLAUDE.md` to enumerate the two guard shapes introduced by this refactor:
  - Active-row mutations (`Promote`, heartbeat extensions, terminal `Delete` in the carve-out paths) carry `AND holder_supervisor_id = $supervisor_id` exactly as today.
  - Non-active-row deletions (retention sweep; Release-path `Delete`) are claimant-guarded by absence (`holder_supervisor_id IS NULL` is enforced by the second CHECK constraint added in Stage 1); the retention sweep is additionally serialized by the scheduler-tick advisory lock, and the Release-path `Delete` is gated by the row-discovery query that only returns `state = 'committed' AND lifetime = 'durable'` rows.
- Update `@blessed-invariant 22` text to match (refer to `state = 'committed' AND lifetime = 'durable'` instead of `held_durable = TRUE`).
- Update `file:CHANGELOG.md` Unreleased section.

### Blessed invariants affected

- **`@blessed-invariant 4` (claimant-guarded release)** — text update required. Active-row mutations stay carrying `AND holder_supervisor_id = $supervisor_id` as today (`Promote`, heartbeat extends, terminal `Delete` in the carve-out paths). Two new categories of non-active-row deletion are introduced — retention sweep and Release-path `Delete` — neither carries the per-row claimant guard. They are guarded instead by (a) the second CHECK constraint added in Stage 1, which enforces `state != 'active' OR holder_supervisor_id IS NOT NULL` AND `state = 'active' OR holder_supervisor_id IS NULL` (so post-Promote rows have `holder_supervisor_id IS NULL` by construction); (b) the scheduler-tick advisory lock serializing the retention sweep across replicas; and (c) the row-discovery query filter for Release-path Delete (`state = 'committed' AND lifetime = 'durable'`). The invariant text in `file:CLAUDE.md` must be updated in Stage 5 to reflect this two-guard-shape model; otherwise the new sweep landing under the unchanged invariant text would read as a literal invariant violation.
- **`@blessed-invariant 22` (held-durable persistence)** — text refreshed to refer to `state = 'committed' AND lifetime = 'durable'`. Substantive behavior preserved.
- **New `@blessed-invariant` candidate** — claim-handle state-transition discipline: only the `Promote` query may transition `active → committed/abandoned`; transitions are claimant-guarded; revival transitions are not permitted at the Go layer. Annotated on the new `code:foundation/spec/claim_handle_state.go` and on `code:foundation/persistence/postgres/claim_handles.go::Promote`. Decide during Stage 5 whether this rises to invariant grade or stays as concept-doc invariant text.

### What stays the same

- Producer-side state machine (`Open / Commit / Abandon / Release` verbs) — unchanged.
- `table:rimsky_claim_holders` table + co-hold mechanic (`concept:claim-co-holdership`) — has its own `state` column at the holder level; that table is not touched.
- Asset URL identity form (`{instance_id}.{asset_alias}`) — unchanged.
- Auto-terminal aggregation logic (counters + recursive walker) — only the post-aggregation step changes (`Delete` → `Promote`).
- `concept:cancel-siblings` algorithm — only the skip-filter expression changes.
- The `code:runtime/abandon_claim.go::abandonOpenedClaim` helper used by the two non-unified carve-outs (`OnAcquireUnavailable` pass/error path; verify-before-run bail path) — these paths still `Delete` directly. Per `concept:auto-terminal` Invariants, those rows are claimant-guarded outside the unified engine; the state-column refactor does not change them.

### Forensics surface (complementary to A+B)

The A+B follow-up added `col:rimsky_lineage.outcome` so `Commit + Abandon + force_cancelled` are queryable from `table:rimsky_lineage`. The state-column refactor adds a parallel surface on `table:rimsky_claim_handles` itself: `state = 'abandoned'` rows are queryable directly without a lineage join.

The two surfaces are complementary:

- `table:rimsky_lineage` answers "what data versions resulted from this instance?" (content-identity-over-time)
- `table:rimsky_claim_handles` (post-refactor) answers "what claims existed in this instance and how did they resolve?" (claim-lifecycle-over-time)

Operators get a row-preservation window of `cfg:retention.claim_handles_trailing = 30d` (default) where forensics queries against the claim_handle layer don't require lineage joins.

---

## Item 4 — Cold-read paydown

Pure refactor. No behavior change. All `@source` / `@agent-contract` / `@blessed-invariant` annotations preserved. All tests pass identically.

### Tier 1 — concrete splits with named boundaries

**`code:runtime/runner_acquire.go` (1096 lines, 18 functions, `tryAcquire` ~250 lines)** → split into four files:

- `runtime/runner_acquire.go` — top-level orchestration entry. `tryAcquire` stays here, shrinks to dispatch into the per-concern files.
- `runtime/runner_acquire_named_locks.go` — named-lock acquisition. `takeNamedAdvisoryLocks`, `acquireOneLock`, `acquireNamedLock`.
- `runtime/runner_acquire_claims.go` — claim acquisition. `acquireClaim`, `evaluateScopeConflict`.
- `runtime/runner_acquire_holders.go` — holder INSERTs. `insertHeldClaimHoldersAtAcquire`, `insertCoHolderClaimHoldersAtAcquire`, `findHoldingSubgraphForAcquirer`.

**`code:runtime/terminal_decision.go::ResolveClaimHandleTerminal` (~190 lines, 8 distinct operations)** → orchestration shell + per-branch helpers extracted in-file (no new file required; the function shrinks to dispatch into helpers in the same file):

- `dispatchDataProcessingTerminal` — DataProcessing-aware Commit branch (CommitCandidate vs Commit).
- `fireProducerVerb` — the verb dispatch (Commit / Abandon) with retry/error handling.
- `writeTerminalLineage` — lineage-row write (`record_kind = 'claim_terminal'` per the A+B refactor).
- `promoteOrDelete` — the state-column write (this is where Item 1's `Promote` lands cleanly; pre-Item-1 it's the `Delete` call).
- `bumpParentCounter` — parent aggregation-counter update.
- `recurseSiblingCancel` — strict.cancel_siblings descent.
- `recurseParentChain` — `resolveParentClaimChain` invocation.

The top-level `ResolveClaimHandleTerminal` reads as the workflow; each helper is bounded.

### Tier 2 — re-measure after Item 1 lands

After Item 1's state-column refactor completes, `code:runtime/subgraph_dispatch.go` (572 lines today) and `code:runtime/auto_terminal.go` (553 lines today) are re-measured.

- If still over the 500-line guideline, split per their top-level concerns. For `auto_terminal.go`, candidate split is `auto_terminal_check.go` (resolution check + lock acquisition) + `auto_terminal_chain.go` (parent-chain recursion) + `auto_terminal_aggregator.go` (holder-state aggregation). For `subgraph_dispatch.go`, the natural split is per dispatch-phase (entry-node absorption / exit-node carry / failure-cancel cascade).
- If Item 1's restructure has pulled them under guideline, leave alone.

### Annotation + behavior preservation

All four files (and any new files spawned from the splits) preserve:

- Every `@source` annotation, with cross-references updated where the source location moved.
- Every `@agent-contract` block, intact and on the same exported surface.
- Every `@blessed-invariant` annotation, on the same load-bearing site.
- Every `@concept:` annotation, on the same load-bearing site.

No tests change behavior. Item 4 is done when:

- All four files (and any new files from the splits) are under the 500-line guideline.
- All function bodies in the touched files are under the 100-line guideline (with case-by-case carve-outs for functions where the natural shape exceeds 100, recorded in CHANGELOG).
- `cmd:make build-all && cmd:make lint && cmd:make test-all` clean.
- `feature-index.md` updated if file layout changed enough to matter.
- `file:CHANGELOG.md` entry under Unreleased.

---

## Item 5 — Multi-replica `sensor-cron` test fixture

### Scope

`code:sensors/sensor-cron/` implements `pg_try_advisory_lock(SENSOR_CRON_KEY)` per-watch for multi-replica safety. Implementation exists; test fixture is single-replica only.

### Test deliverable

`code:sensors/sensor-cron/multi_replica_test.go` (new) or an extension of `code:test/smoke/`:

- Boots two `sensor-cron` instances against the same Postgres + same watch set.
- Drives a fire window where both replicas attempt to acquire the advisory lock.
- Asserts:
  - Exactly one fires the cron action per window.
  - The other backs off cleanly (no spurious retries, no duplicate observations land at the control-api).
- Fail-injection: kill the holding replica mid-window; assert the other picks up at the next tick.

### Done

Scenario test passes consistently (50+ consecutive runs clean, with the standard `cmd:go test -race -count=50` discipline applied to the new file). Documented in `file:CHANGELOG.md` Unreleased.

---

## Item 6 — Asset/lineage end-to-end coverage uniformity

### Phase 1 — classification dispatch (first execute-plan step)

Audit `code:test/scenarios/asset/*` and `code:test/scenarios/lineage/*` (~20 files). Classify each test:

- **Shape-pinning** — verifies struct fields / SQL column round-trips without booting the harness. Appropriate for property-pinning where harness boot is overhead.
- **End-to-end** — boots Postgres + scheduler + supervisor + control-api + bundled producer/executor and drives a real cascade.
- **Mixed** — does both in one test.

For each shape-pinning or mixed test, recommend one of three dispositions:

- **Keep as-is** — unit-level coverage is appropriate for this property.
- **Upgrade to end-to-end** — the load-bearing property needs the full stack; rewrite as end-to-end.
- **Split** — keep the unit-level pin (it's cheap) + add an end-to-end companion (the load-bearing property needs both signals).

Output: classification document at `.ok-planner/plans/<plan>-notes.md`, with a per-file table:

```
| File | Current shape | Disposition | Rationale |
|---|---|---|---|
| test/scenarios/asset/foo_test.go | shape-pinning | upgrade | … |
```

### Phase 2 — execute the dispositions

Per the classification document:

- **Upgrades** — rewrite the named tests as end-to-end (boot the harness, drive the cascade, assert at the same boundary the original test asserted).
- **Splits** — add the end-to-end companion test alongside the existing shape-pinning test.
- **Keep-as-is** — no action.

Estimate from the sketch: 5–10 of ~20 tests will need upgrade or companion. The classification is the source of truth; the estimate is just for scoping.

### Done

- Classification document complete (every file in scope classified).
- Every upgrade / split disposition executed.
- `cmd:make test-all` clean.
- `file:CHANGELOG.md` Unreleased entry summarizing the audit findings + actions taken.

---

## Testing strategy

### Item 1

The biggest test surface. Required signals at each stage:

- **Stage 1 (additive)** — `cmd:make test-all` clean. Specifically: `code:runtime/runner_acquire_test.go`, `code:runtime/terminal_decision_test.go`, `code:runtime/auto_terminal_test.go`, `code:runtime/orphan_reaper_test.go`, `code:runtime/instance_termination_test.go`, plus scenario tests under `code:test/scenarios/asset/`, `code:test/scenarios/lineage/`, `code:test/scenarios/run_tree/`, `code:test/scenarios/forensics/`.
- **Stage 2 (reader cutover)** — same coverage; verifies dual-read consistency.
- **Stage 3 (promote-not-delete)** — scenario tests' "row gone after terminal" assertions flip to "row state = '…' after terminal; row gone after retention-sweep advances clock past cutoff." New test: `code:runtime/sweep_claim_handle_retention_test.go` exercising the sweep cutoff predicate.
- **Stage 4 (drop bool)** — full coverage clean.
- **Race signal:** `cmd:go test -race -count=3 ./foundation/persistence/postgres/... ./runtime/...` for stages 1, 3, 4.

### Item 4

Pure refactor; all existing tests must pass identically. No new tests required. `cmd:make test-all` clean is the only signal.

### Items 2, 3, 5, 6

- **Item 2** — reviewer pass; no new tests required (the work being reviewed already has its own tests).
- **Item 3** — for each flake, 50–100 consecutive runs under load clean after the fix.
- **Item 5** — new scenario test (`multi_replica_test.go`); 50+ consecutive runs under `cmd:go test -race -count=50` clean.
- **Item 6** — per the classification: upgraded tests run end-to-end and pass; existing tests preserved.

---

## Risks

- **Item 1 dominates the schedule.** The 5-stage cutover discipline parallels cycles 8–10's run-tree cutover. Each stage is a working-tree quiescence point; budget accordingly.
- **Stage 3's retention-sweep test surface is novel.** The sweep introduces time-dependent behavior; scenario tests need clock advancement (using the existing `code:foundation/shared.Clock` test fixture). Risk: tests that don't advance the clock correctly will see rows that shouldn't have been swept yet, OR vice versa.
- **Item 4 done before Item 1 wastes work.** Sequencing in this spec puts Item 4 after Item 1 explicitly.
- **Cold-read paydown is invisible work to reviewers.** No new behavior; the diff is "this function moved to that file." Item 4's value is realized by future contributors; reviewer should focus on annotation preservation and equivalence.
- **Smoke flakes may turn out to be `testcontainers` parallelism artifacts.** If so, the fix is fixture-level (serialize the test) rather than production-code-level. That's still valid resolution.
- **Multi-replica `sensor-cron` test fixture can itself be flaky.** Two replicas + Postgres + advisory locks + fire windows = a test that needs careful bounding. The 50-run-clean signal protects against shipping a flaky test.
- **Item 6's audit may surface more work than the classification's scope suggests.** Audits expand. If the audit reveals coverage gaps beyond asset/lineage (e.g., in `run_tree/` or `forensics/`), those become out-of-scope for this spec — note them, defer to a follow-up sketch.
- **Concept catalog updates risk drift if Stage 5 is skipped or rushed.** The catalog is the durable design log; concept files in `concepts/` must reflect the post-refactor reality, not the pre-refactor reality, before Item 1 is done. This is part of the project's after-code-changes discipline (`file:.claude/rules/rules.md`).

---

## What this is not

- **Not a v1 commitment.** Pre-v1 paydown + sharpening. Doesn't decide v1's data model, observability surface, or operator surface.
- **Not new feature delivery.** No new protocol surfaces, no new bundled services, no new control-api endpoints.
- **Not a held-durable lifecycle behavior change.** Item 1 changes the modeling, not the behavior.
- **Not coordinated with the 2026-05-16 traceability sketch.** Trace-context columns on `table:rimsky_claim_handles` are the traceability sketch's concern, on its own schedule. Pre-v1 authorizes two migrations on the same table.
- **Not a commit-strategy prescription.** Commits happen at execute-plan time, after work and review.
- **Not a separate `rimsky_assets` table** (Option D from the held-durable refactor discussion was articulated and rejected; this spec uses the state-column approach).
