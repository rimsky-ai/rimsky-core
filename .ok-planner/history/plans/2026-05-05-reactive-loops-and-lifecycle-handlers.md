# Reactive Loops + Lifecycle Handlers Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`

**Goal:** Add four declarable lifecycle-handler slots to node templates (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`), a `last_outcome` resolution-flavor column on `rimsky_nodes`, per-emit `frame: in | next` configurability on every invalidate emit, and a `last_progress_at`-based refinement of `frame_timeout_ms` semantics. Today's lazy + Changed-gated cascade is preserved end-to-end.

**Architecture:** New persistent column `rimsky_nodes.last_outcome` written at every transition that lands a terminal-for-this-frame state. Handler dispatch logic in `foundation/integration/runner_acquire.go` (Unavailable path) and `foundation/integration/runner_terminal.go` (Complete/Blocked/Errored paths) routes `resolve:` and emits optional `invalidate:` per `frame:`. `Abandon` calls clean up producer-side state for already-Available claims under `pass`/`error` resolutions, matching today's `handleOrphanedClaim` semantics. The cascade-firing gate moves from `if t.Changed` to `if last_outcome == fresh_changed` inside the existing `applyTerminalComplete` tx; under the default `by_changed` handler the gate is functionally identical to today.

**Tech Stack:** Go 1.x in three modules (`foundation/`, `protocols/`, root `modeling/`); Postgres via `jackc/pgx/v5`; SQLite via `modernc.org/sqlite`; HTTP routing via `go-chi/chi`; logging via stdlib `log/slog`. Tests use `testcontainers-go` for Postgres scenario tests; `go test ./...`, `make lint`, and TypeScript executor build (`cd executors/claude-agent && npm install && npm test && npm run build`) are mandatory verification gates per `.claude/rules/rules.md`.

---

## Reading guide for the implementer

You are working in a fresh Claude Code session against the rimsky submodule. Project root for this plan is `/Users/patrick/Documents/projects/research/verantel/submodules/rimsky`. Treat that as your working directory; all paths below are relative to it unless otherwise stated.

Before starting, do these three things:

1. Read the spec at `.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`. The plan implements the spec exactly; if a task seems to contradict the spec, the spec wins — pause and surface the contradiction in the implementation notes.
2. Read `.claude/rules/rules.md` and `.claude/rules/cold-read-cheatsheet.md`. The rules' "After Code Changes — Required Final Step" applies after every Go change in this plan; cold-read conventions govern the new code.
3. Read `CLAUDE.md` for the post-Phase-5 layer-crystallization layout (foundation / protocols / modeling) and the depguard import rules.

Today's runtime behavior must be preserved for templates that don't declare lifecycle handlers — every existing scenario test should continue to pass. Migration is pre-v1 demolition (per `rules.md`); new columns are simple `ALTER TABLE` additions with safe defaults; no compat shim, no backfill. If you discover the implementation surface is incomplete or contradicts the codebase, surface it before continuing — do not silently narrow scope.

After every Go change, run `go build ./...` and `go test ./...` from the repo root. After every change to a Go package that's part of the supervisor / scheduler / queue paths or the persistence layer, also run `make lint`. After every protocol change, run `make proto-gen` first. Per `rules.md` "Race-sensitive paths": run `-race -count=3` on the supervisor, scheduler, and queue paths whenever you modify them.

---

## Task 1: Verify clean baseline

**Files to touch:** none (verification only).

**Steps:**

1. From the repo root, run `go build ./...` and confirm it exits 0.
2. Run `go test ./...` (this includes scenario tests via testcontainers; Docker must be running). Confirm it exits 0. Note any flakes or pre-existing failures so you can distinguish them from regressions later.
3. Run `make lint`. Confirm it exits 0.
4. Run `cd executors/claude-agent && npm install && npm test && npm run build && cd -`. Confirm each step exits 0.

**Verification:**
```bash
go build ./... && go test ./... && make lint
```
All three must exit 0. If anything fails on a clean tree, stop and surface to the user — the plan assumes a clean baseline.

---

## Task 2: Add `LastOutcome` enum

**Files to touch:** `modeling/shared/types.go` (existing file; same package as `NodeState`).

**Steps:**

1. Open `modeling/shared/types.go`.
2. Below the `NodeState` const block (after the existing `NodeStateFailed` constant), add:

   ```go
   // LastOutcome is the resolution flavor recorded on rimsky_nodes for
   // terminal-for-this-frame transitions. Distinct from NodeState; the
   // node's state machine is unchanged. last_outcome lives on the
   // rimsky_nodes row alongside state and is written by the same
   // transition that lands the node in fresh or failed.
   //
   // Values are persisted as TEXT under both Postgres and SQLite. NULL
   // means "no outcome recorded yet" (legacy fresh nodes pre-migration).
   //
   // See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §2.2.
   type LastOutcome string

   const (
       LastOutcomeFreshChanged   LastOutcome = "fresh_changed"
       LastOutcomeFreshUnchanged LastOutcome = "fresh_unchanged"
       LastOutcomePassed         LastOutcome = "passed"
       LastOutcomePureCascade    LastOutcome = "pure_cascade"
       LastOutcomeFailed         LastOutcome = "failed"
   )
   ```

**Verification:**
```bash
go build ./...
```
Must exit 0. The new type is unused at this point; that's fine — subsequent tasks introduce callers.

---

## Task 3: Add new `TransitionReason` kinds

**Files to touch:** `foundation/cascade/state.go`.

**Steps:**

1. Open `foundation/cascade/state.go`.
2. In the `var (...)` block declaring existing reasons (around line 19), add four new exported reasons:

   ```go
   // ReasonAcquirePass — stale → fresh, last_outcome=passed.
   // on_acquire_unavailable handler resolved pass; the node transitions
   // without invoking the executor and without firing the cascade.
   ReasonAcquirePass = TransitionReason{Kind: "acquire_pass"}

   // ReasonHandlerComplete — running → fresh.
   // on_executor_complete handler resolved. Subsumes
   // ReasonWorkCompleted for new code paths; the old constant is kept
   // as a deprecated alias for one cycle to ease the doc / annotation
   // migration.
   ReasonHandlerComplete = TransitionReason{Kind: "handler_complete"}

   // ReasonHandlerError — running → stale or running → failed.
   // on_executor_blocked / on_executor_errored handler routed
   // through error_types policy chain; specific transition follows
   // the policy outcome (retry → stale; invalidate → stale; give_up
   // → failed).
   ReasonHandlerError = TransitionReason{Kind: "handler_error"}

   // ReasonHandlerPass — running → fresh, last_outcome=passed.
   // on_executor_blocked / on_executor_errored handler resolved pass
   // (template explicitly opts to ignore the terminal).
   ReasonHandlerPass = TransitionReason{Kind: "handler_pass"}
   ```

3. Keep `ReasonWorkCompleted` defined unchanged. Add a doc comment above it noting it's deprecated for new callers and replaced by `ReasonHandlerComplete`.

**Verification:**
```bash
go build ./...
```
Must exit 0.

---

## Task 4: Extend `NextState` transition table

**Files to touch:** `foundation/cascade/state.go`.

**Steps:**

1. Open `foundation/cascade/state.go`. Locate the `NextState` function (~line 56).
2. In the `case shared.NodeStateStale:` branch, add after the existing reasons:

   ```go
   if reason.Kind == "acquire_pass" {
       return shared.NodeStateFresh, nil
   }
   ```

3. In the `case shared.NodeStateRunning:` branch, add after the existing reasons:

   ```go
   if reason.Kind == "handler_complete" {
       return shared.NodeStateFresh, nil
   }
   if reason.Kind == "handler_pass" {
       return shared.NodeStateFresh, nil
   }
   // handler_error transitions follow the policy chain; expressed as
   // policy_retry / policy_invalidate / policy_give_up at the call site
   // after the policy chain resolves. ReasonHandlerError itself is not
   // a direct NextState input — it's the audit-log reason recorded
   // when a handler routes through error_types; the actual state
   // transition uses the policy-chain reasons that already exist.
   ```

   The `handler_error` reason isn't a direct transition reason — the policy chain produces one of `policy_retry` / `policy_invalidate` / `policy_give_up` and `NextState` already handles those. The constant exists for the audit log only. Document this in a comment above the new cases.

4. Confirm blessed invariant 1 holds: no `running → running` short-circuit; no `from == to` collapse. The existing implementation already enforces this via `ErrIllegalTransition`; do not weaken it.

**Verification:**
```bash
go build ./...
```
Must exit 0.

---

## Task 5: Update state-machine unit tests

**Files to touch:** `foundation/cascade/state_test.go`.

**Steps:**

1. Open `foundation/cascade/state_test.go`. Read the existing test patterns to match style.
2. Add unit tests for the four new transition reasons:
   - `TestNextState_AcquirePass` — `stale → fresh` under `ReasonAcquirePass`. Other states under this reason return `ErrIllegalTransition`.
   - `TestNextState_HandlerComplete` — `running → fresh` under `ReasonHandlerComplete`. Other states return `ErrIllegalTransition`.
   - `TestNextState_HandlerPass` — `running → fresh` under `ReasonHandlerPass`. Other states return `ErrIllegalTransition`.
   - `TestNextState_HandlerErrorIsAuditOnly` — calling `NextState` with `ReasonHandlerError` from any state returns `ErrIllegalTransition` (it's not a direct transition reason; it's an audit-log marker).
3. Add a regression test asserting that `LastOutcome` constants serialize to the expected strings (e.g., `string(LastOutcomePassed) == "passed"`). This protects the column-value contract.

**Verification:**
```bash
go test ./foundation/cascade/... -count=1
```
All tests must pass.

---

## Task 6: Add Postgres migration for `last_outcome`

**Files to touch:** `foundation/persistence/postgres/migrations/004-last-outcome-and-progress.sql` (new).

**Steps:**

1. Create `foundation/persistence/postgres/migrations/004-last-outcome-and-progress.sql` with:

   ```sql
   -- 2026-05-05 last_outcome + last_progress_at: support for the
   -- reactive-loops + lifecycle-handlers spec.
   -- See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §2.4.

   ALTER TABLE rimsky_nodes
       ADD COLUMN last_outcome TEXT;

   ALTER TABLE rimsky_frames
       ADD COLUMN last_progress_at TIMESTAMPTZ NOT NULL DEFAULT now();
   ```

2. Confirm `embed.go` in the same directory uses a `//go:embed *.sql` glob (not an explicit list); if it lists files explicitly, add `004-last-outcome-and-progress.sql` to the list. (Read the existing file to verify.)

**Verification:**
```bash
go build ./...
```
Must exit 0. Migrations don't run yet; this is a static-asset add.

---

## Task 7: Add SQLite migration for `last_outcome`

**Files to touch:** `foundation/persistence/sqlite/migrations/002-last-outcome-and-progress.sql` (new).

**Steps:**

1. Inspect `foundation/persistence/sqlite/migrations/`. Today there is only `001-initial.sql` (per `.ok-planner/specs/2026-05-02-persistence-pluggable-and-unified-image-design.md` §5.1, SQLite started with one consolidated migration). The next migration is `002-...`.
2. Create `foundation/persistence/sqlite/migrations/002-last-outcome-and-progress.sql` with:

   ```sql
   -- 2026-05-05 last_outcome + last_progress_at: support for the
   -- reactive-loops + lifecycle-handlers spec.
   -- See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §2.4.
   --
   -- Postgres uses TIMESTAMPTZ; SQLite stores ISO-8601 strings via the
   -- existing time-handling pattern (per persistence-pluggable spec §6.3).

   ALTER TABLE rimsky_nodes
       ADD COLUMN last_outcome TEXT;

   ALTER TABLE rimsky_frames
       ADD COLUMN last_progress_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
   ```

3. Confirm SQLite `embed.go` uses a glob; if explicit, add the new file.

**Verification:**
```bash
go build ./...
```
Must exit 0.

---

## Task 8: Run migrations against test environments

**Files to touch:** none (verification only).

**Steps:**

1. Run the persistence-driver migration tests. The Postgres tests use testcontainers; the SQLite tests use a file in a temp directory.
   ```bash
   go test ./foundation/persistence/postgres/... -count=1 -run Migrate
   go test ./foundation/persistence/sqlite/... -count=1 -run Migrate
   ```
2. If either set fails because the migrate test fixture lists migrations explicitly, update the fixture to expect the new file.

**Verification:**
Both commands exit 0.

---

## Task 9: Extend `Nodes.UpdateState` signature to accept `LastOutcome`

**Files to touch:**
- `foundation/persistence/Nodes` interface (probably in `foundation/persistence/nodes.go` or sibling — find via `grep -rn "UpdateState" foundation/persistence/`).
- `foundation/persistence/postgres/nodes.go`.
- `foundation/persistence/sqlite/nodes.go`.
- All callers of `UpdateState` (find via `grep -rn "Nodes().UpdateState\|nodes.UpdateState" foundation/ modeling/`).

**Steps:**

1. Read the `Nodes` interface declaration. The existing signature is:
   ```go
   UpdateState(ctx context.Context, id shared.UUID, state shared.NodeState, reason cascade.TransitionReason, tx persistence.Tx) error
   ```
2. Change it to accept a new `lastOutcome shared.LastOutcome` parameter — the empty string `""` means "do not write the column" (preserving NULL for nodes transitioning into states where last_outcome doesn't apply, e.g., `→ stale`, `→ running`):
   ```go
   UpdateState(ctx context.Context, id shared.UUID, state shared.NodeState, reason cascade.TransitionReason, lastOutcome shared.LastOutcome, tx persistence.Tx) error
   ```
3. Update the Postgres impl in `foundation/persistence/postgres/nodes.go::enforceAndUpdate`. Inside the existing UPDATE statement, add a CASE for `last_outcome`:
   - When `lastOutcome != ""`, write it.
   - When `lastOutcome == ""`, leave the column unchanged (do NOT NULL it out — terminal-for-this-frame transitions go through this method and writing NULL would lose the prior outcome on stale-then-fresh round-trips).

   Implementation sketch (substitute against the existing query):
   ```go
   var outcomeArg any
   if lastOutcome == "" {
       outcomeArg = nil
   } else {
       outcomeArg = string(lastOutcome)
   }
   _, err = ex.Exec(ctx,
       `UPDATE rimsky_nodes
          SET state = $2,
              updated_at = NOW(),
              assigned_supervisor_id = CASE WHEN $2 = 'running' THEN assigned_supervisor_id ELSE NULL END,
              last_heartbeat_at      = CASE WHEN $2 = 'running' THEN last_heartbeat_at      ELSE NULL END,
              frame_id               = CASE WHEN $2 = 'fresh'   THEN NULL ELSE frame_id END,
              last_outcome           = COALESCE($3::text, last_outcome)
        WHERE id = $1`,
       id, string(state), outcomeArg,
   )
   ```
   The `COALESCE($3::text, last_outcome)` preserves the existing column value when the caller passes empty; explicit values overwrite.

4. Apply the symmetric change in `foundation/persistence/sqlite/nodes.go`. SQLite's COALESCE works the same way; the `::text` cast becomes a no-op (SQLite uses dynamic typing).

5. Update every caller of `UpdateState`. The natural defaults:
   - Transitions to `running` → pass `""` (no outcome).
   - Transitions to `stale` → pass `""` (no outcome; clearing isn't necessary — see step 3 above).
   - Transitions to `fresh` via `ReasonWorkCompleted` (today's path) → pass `LastOutcomeFreshChanged` if `t.Changed` else `LastOutcomeFreshUnchanged`. **Note:** Task 17 will replace this call with the handler-driven outcome; for now keep the by_changed mapping inline.
   - Transitions to `fresh` via `ReasonPureCascade` → pass `LastOutcomePureCascade`.
   - Transitions to `failed` via `ReasonPolicyGiveUp` / `ReasonDispatchImpossible` → pass `LastOutcomeFailed`.
   - Transitions to `fresh` via `ReasonOperatorReset` / `ReasonOperatorInvalidate` from `failed` → pass `""` (going stale, no outcome).

   You'll find callers in `foundation/integration/runner_terminal.go`, `foundation/integration/runner_acquire.go`, `foundation/integration/cascade_invalidate.go`, `foundation/integration/cascade_recalculate.go`, `foundation/integration/on_error.go`, `modeling/scheduler/`, `modeling/controlapi/`. Run `grep -rn "Nodes().UpdateState" foundation/ modeling/` to enumerate; update each.

6. Update test files that call `UpdateState` directly (the persistence-package unit tests and any scenario harness helpers).

**Verification:**
```bash
go build ./...
go test ./foundation/persistence/... ./foundation/cascade/... -count=1
```
Both must exit 0. If callers fail compilation, you've missed one — fix and re-run.

---

## Task 10: Update `Frames` to track `last_progress_at`

**Files to touch:**
- `foundation/persistence/postgres/frames.go`.
- `foundation/persistence/sqlite/frames.go`.
- `modeling/frame/engine.go` (queries that read `rimsky_frames`).

**Steps:**

1. Read `foundation/persistence/postgres/frames.go`. Locate the row-mapping struct and the methods that INSERT or UPDATE `rimsky_frames`.
2. Add `LastProgressAt time.Time` to the row-mapping type, and read it from `SELECT` queries.
3. Add a new repository method:
   ```go
   // RefreshProgress updates rimsky_frames.last_progress_at to NOW() for
   // the given frame. Called by the supervisor's terminal handler and
   // the scheduler's tick on every state-transition write that carries
   // the frame's id, so the frame_timeout_ms metric measures
   // no-progress-in-window rather than frame age.
   //
   // See spec §7.
   RefreshProgress(ctx context.Context, frameID shared.UUID, tx persistence.Tx) error
   ```
4. Implement under both Postgres (`now()`) and SQLite (`strftime` ISO-8601 string).
5. Symmetric SQLite changes.
6. Locate the existing `frame_timeout_ms` evaluation query (likely in `modeling/frame/engine.go` or `producer.go`). Today's predicate computes age from `opened_at` (or equivalent). Change it to compute from `last_progress_at`:
   ```sql
   -- Postgres
   SELECT id FROM rimsky_frames
    WHERE state = 'running'
      AND now() - last_progress_at > make_interval(milliseconds := frame_timeout_ms)
   ```
   The SQLite equivalent uses string arithmetic on the ISO-8601 column; follow the existing time-handling pattern in the file.

**Verification:**
```bash
go build ./...
go test ./foundation/persistence/... ./modeling/frame/... -count=1
```
Both must exit 0.

---

## Task 11: Wire `RefreshProgress` into state-transition write path

**Files to touch:** `foundation/persistence/postgres/nodes.go`, `foundation/persistence/sqlite/nodes.go`.

**Steps:**

1. In each driver's `enforceAndUpdate` (where the `UPDATE rimsky_nodes ...` statement runs), after the UPDATE, if the node has a non-NULL `frame_id`, call `RefreshProgress(ctx, frameID, tx)`. The frame_id comes from the row before the UPDATE — read it via the existing `SELECT state FROM rimsky_nodes WHERE id = $1 FOR UPDATE` (extend that SELECT to also return `frame_id`).
2. Note that the `frame_id` column gets set to NULL when transitioning to `fresh` (per the existing CASE in step 3 of Task 9). To capture progress for the closing frame, read `frame_id` BEFORE the UPDATE, then call `RefreshProgress` with the captured value AFTER the UPDATE commits — or pass the captured value into the same tx so the refresh runs alongside.
3. Both drivers' `enforceAndUpdate` need this wiring. Keep the change inside the same tx as the UPDATE.

**Verification:**
```bash
go build ./...
go test ./foundation/persistence/... -count=1 -race -run Frame
```
Must exit 0.

---

## Task 12: Add lifecycle-handler types to `modeling/node/template.go`

**Files to touch:** `modeling/node/template.go`.

**Steps:**

1. Open `modeling/node/template.go`. Locate `TemplateNodeDef` (line ~54).
2. Add four new fields to the struct, after `ErrorTypes`:
   ```go
   OnAcquireUnavailable *OnAcquireUnavailableHandler `yaml:"on_acquire_unavailable,omitempty" json:"on_acquire_unavailable,omitempty"`
   OnExecutorComplete   *OnExecutorCompleteHandler   `yaml:"on_executor_complete,omitempty"   json:"on_executor_complete,omitempty"`
   OnExecutorBlocked    *OnExecutorTerminalHandler   `yaml:"on_executor_blocked,omitempty"    json:"on_executor_blocked,omitempty"`
   OnExecutorErrored    *OnExecutorTerminalHandler   `yaml:"on_executor_errored,omitempty"    json:"on_executor_errored,omitempty"`
   ```
3. Add the new handler types in the same file (or a sibling `handlers.go` if you prefer; cold-read prefers grouping by feature). Sketch:

   ```go
   // OnAcquireUnavailableHandler declares the supervisor's behavior when
   // any required claim's Open returns Unavailable. See spec §3.
   type OnAcquireUnavailableHandler struct {
       Resolve    string             `yaml:"resolve" json:"resolve"`           // pass | retry | error
       ErrorClass string             `yaml:"error_class,omitempty" json:"error_class,omitempty"` // required when resolve=error
       Invalidate *HandlerInvalidate `yaml:"invalidate,omitempty" json:"invalidate,omitempty"`
   }

   // OnExecutorCompleteHandler declares the supervisor's behavior on a
   // Complete terminal. See spec §3.
   type OnExecutorCompleteHandler struct {
       Resolve    string             `yaml:"resolve" json:"resolve"`           // by_changed | always_propagate | never_propagate
       Invalidate *HandlerInvalidate `yaml:"invalidate,omitempty" json:"invalidate,omitempty"`
   }

   // OnExecutorTerminalHandler declares behavior on a Blocked or
   // Errored terminal. See spec §3.
   type OnExecutorTerminalHandler struct {
       Resolve    string             `yaml:"resolve" json:"resolve"`           // error | pass
       ErrorClass string             `yaml:"error_class,omitempty" json:"error_class,omitempty"` // required when resolve=error
       Invalidate *HandlerInvalidate `yaml:"invalidate,omitempty" json:"invalidate,omitempty"`
   }

   // HandlerInvalidate is the optional invalidate-emit slot on every
   // lifecycle handler. Fires unconditionally when the handler runs;
   // orthogonal to resolve. See spec §3.5.
   type HandlerInvalidate struct {
       Targets []string `yaml:"targets" json:"targets"`
       Frame   string   `yaml:"frame,omitempty" json:"frame,omitempty"` // in | next; default next
   }

   // Resolve constants per handler. The validator at template-deploy
   // rejects out-of-vocabulary combinations.
   const (
       ResolvePass             = "pass"
       ResolveRetry            = "retry"
       ResolveError            = "error"
       ResolveByChanged        = "by_changed"
       ResolveAlwaysPropagate  = "always_propagate"
       ResolveNeverPropagate   = "never_propagate"

       FrameIn   = "in"
       FrameNext = "next"

       SelfTarget = "self"
   )
   ```

**Verification:**
```bash
go build ./...
```
Must exit 0.

---

## Task 13: Add lifecycle-handler validation to `modeling/node/template_validator.go`

**Files to touch:** `modeling/node/template_validator.go`.

**Steps:**

1. Open `modeling/node/template_validator.go`. Locate `validateErrorTypes` (line ~189) and the surrounding per-node validation loop.
2. Add new validation functions called from the same per-node loop:
   - `validateOnAcquireUnavailable` — checks `Resolve ∈ {pass, retry, error}`; if `error`, `ErrorClass` is non-empty and references a key in `n.ErrorTypes` (or is one of the built-in classes). Validates any declared `Invalidate` block (see below).
   - `validateOnExecutorComplete` — checks `Resolve ∈ {by_changed, always_propagate, never_propagate}`. Validates `Invalidate`.
   - `validateOnExecutorBlocked` and `validateOnExecutorErrored` — checks `Resolve ∈ {error, pass}`; `ErrorClass` required when `Resolve=error`.
3. Add a helper `validateHandlerInvalidate(invalidate *HandlerInvalidate, n TemplateNodeDef, declared map[string]int, base string, res *ValidationResult)`:
   - If `Invalidate` is nil, skip.
   - Reject empty `Targets`.
   - Each target must be `SelfTarget` (literal `"self"`) or a declared node type (look up in `declared`).
   - `Frame` must be `""`, `FrameIn`, or `FrameNext`. Empty defaults to `FrameNext`.
4. Reject handlers that have neither `Resolve` nor `Invalidate` (per spec §3.6 — "an empty handler has no effect").
5. Add unit tests in `modeling/node/template_validator_test.go` covering: valid handler shapes; out-of-vocab `resolve`; missing `error_class` when `resolve=error`; invalid target name; invalid `frame` value; empty handler rejected.

**Verification:**
```bash
go test ./modeling/node/... -count=1
```
Must exit 0.

---

## Task 14: Discriminate Unavailable in `acquireClaim` / `acquireOneLock`

**Files to touch:** `foundation/integration/runner_acquire.go`.

**Steps:**

1. Open `foundation/integration/runner_acquire.go`. Locate `acquireClaim` (~line 380) and `acquireOneLock` (above it).
2. Today, both return `(AcquiredLock, bool, error)` where `bool=false` conflates "Unavailable" with conflicted / non-eligible / scope-conflict bails. Introduce a new return type that discriminates:
   ```go
   type openResult int

   const (
       openResultAcquired   openResult = iota // success
       openResultUnavailable                  // producer returned Available=false
       openResultBail                         // any other reason — eligibility, conflict, etc.
   )
   ```
3. Change `acquireClaim` and `acquireOneLock` to return `(AcquiredLock, openResult, error)`. The Unavailable branch (`if !outcome.Available`) returns `openResultUnavailable`; conflict / eligibility branches return `openResultBail`; success returns `openResultAcquired`.
4. Update `tryAcquire` to consume the new return. The per-claim loop:
   ```go
   for _, sp := range specs {
       al, res, err := acquireOneLock(ctx, args, tx, sp, cand, heartbeatInterval, heldSubgraphs)
       if err != nil {
           return acquisition{}, false, err
       }
       if res == openResultUnavailable {
           // Capture state for the unavailable-handler path: we have
           // acquiredLocks (already-Available), and we know the
           // unavailable spec was sp.
           return acquisition{
               // ... carry over relevant fields and the partial list ...
               Locks:           acquiredLocks,
               UnavailableSpec: sp,
           }, false, errAcquireUnavailable
       }
       if res == openResultBail {
           return acquisition{}, false, nil
       }
       acquiredLocks = append(acquiredLocks, al)
   }
   ```
5. Add a sentinel error `errAcquireUnavailable = fmt.Errorf("supervisor: acquire bailed on Unavailable claim (sentinel)")`. Like `errTryAcquireRollback`, it's not a real error to surface — the outer acquisition machinery interprets it.
6. The acquisition struct needs to carry the partial-acquired list and the unavailable spec out across the rollback. Add fields to `acquisition`:
   ```go
   // PartialLocks: locks that successfully Open'd before the
   // Unavailable claim. Captured only when the acquisition path took
   // the Unavailable branch; the outer caller uses these for Abandon
   // cleanup under on_acquire_unavailable resolutions.
   PartialLocks    []AcquiredLock
   UnavailableSpec locks.ClaimSpec  // the spec whose Open returned Unavailable
   ```
7. Update `tryAcquireWithTx` to recognize `errAcquireUnavailable` and propagate the partial-acquired list:
   ```go
   if err == errAcquireUnavailable {
       // tx still rolls back via Postgres semantics; the partial
       // list is in acq for the outer caller to use.
       return acq, false, errAcquireUnavailable
   }
   ```
8. The caller in `runner.go` (or wherever the dispatch loop lives) gets a third branch for `errAcquireUnavailable` — Task 15 implements it.

**Verification:**
```bash
go build ./...
go test ./foundation/integration/... -count=1 -race
```
Must exit 0. Tests covering Unavailable scenarios should still pass under default behavior (silent retry); the new branch is wired in Task 15.

---

## Task 15: Implement `on_acquire_unavailable` handler dispatch

**Files to touch:** `foundation/integration/runner_acquire.go`, possibly `foundation/integration/runner.go` or wherever the dispatch loop lives (find via `grep -n "tryAcquireWithTx\|errTryAcquireRollback" foundation/integration/`).

**Steps:**

1. Locate the caller of `tryAcquireWithTx`. The existing pattern handles `errTryAcquireRollback` as "skip this candidate." Add a new branch for `errAcquireUnavailable`:
   ```go
   if err == errAcquireUnavailable {
       handleAcquireUnavailable(ctx, args, acq, cand)
       continue
   }
   ```
2. Implement `handleAcquireUnavailable`:
   ```go
   func handleAcquireUnavailable(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) {
       // Look up the node's on_acquire_unavailable handler from the template.
       // Find the template via instance lookup (cf. tryAcquire's existing
       // template lookup pattern).
       handler := lookupOnAcquireUnavailable(ctx, args, cand)
       if handler == nil || handler.Resolve == node.ResolveRetry {
           // Default behavior: today's silent retry. The acquisition
           // tx already rolled back; nothing more to do.
           return
       }
       // Abandon already-Available producer state. Matches handleOrphanedClaim.
       for _, lk := range acq.PartialLocks {
           if lk.Store != nil {
               scope := claimScope(lk)
               address := claimAddress(lk)
               claimID := locks.ClaimID(lk.LockHolderID.String())
               if err := lk.Store.Abandon(ctx, claimID, scope, address); err != nil {
                   args.Logger.Warn("handleAcquireUnavailable: Abandon failed",
                       "store", storeNameForSpec(lk.Spec), "error", err.Error())
               }
           }
       }
       // Apply resolution in a fresh tx.
       switch handler.Resolve {
       case node.ResolvePass:
           applyAcquirePass(ctx, args, cand, handler)
       case node.ResolveError:
           applyAcquireError(ctx, args, cand, handler)
       }
   }
   ```
3. `applyAcquirePass`:
   ```go
   func applyAcquirePass(ctx context.Context, args RunArgs, cand persistence.Candidate, h *node.OnAcquireUnavailableHandler) {
       _ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
           if err := args.Persist.Nodes().UpdateState(ctx, cand.NodeID,
               shared.NodeStateFresh, cascade.ReasonAcquirePass,
               shared.LastOutcomePassed, tx); err != nil {
               return err
           }
           return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
               NodeID: &cand.NodeID, InstanceID: &cand.InstanceID,
               Kind: "state_transition",
               Payload: map[string]any{
                   "from": "stale", "to": "fresh", "reason": "acquire_pass",
                   "last_outcome": "passed",
               },
           }, tx)
       })
       // Fire optional handler.invalidate after commit.
       if h.Invalidate != nil {
           emitHandlerInvalidate(ctx, args, cand, h.Invalidate)
       }
   }
   ```
4. `applyAcquireError`:
   ```go
   func applyAcquireError(ctx context.Context, args RunArgs, cand persistence.Candidate, h *node.OnAcquireUnavailableHandler) {
       // Route through error_types[h.ErrorClass].policy via the
       // existing on_error machinery in foundation/integration/on_error.go.
       // Treat this as if the executor returned Errored with class h.ErrorClass.
       routeErrorClass(ctx, args, cand, h.ErrorClass, map[string]any{
           "source": "on_acquire_unavailable",
       })
       // Fire optional handler.invalidate after error routing.
       if h.Invalidate != nil {
           emitHandlerInvalidate(ctx, args, cand, h.Invalidate)
       }
   }
   ```
   The `routeErrorClass` helper is the existing on_error.go path. Find and reuse; do not duplicate the policy-chain logic.
5. `emitHandlerInvalidate`: a helper that resolves `Targets` (including `self` → the current node's type) to node UUIDs and fires `InvalidateNode` per `Frame` (default `FrameNext`):
   ```go
   func emitHandlerInvalidate(ctx context.Context, args RunArgs, cand persistence.Candidate, inv *node.HandlerInvalidate) {
       // Resolve targets to node UUIDs within the same instance.
       targets := resolveHandlerTargets(ctx, args, cand, inv.Targets)
       useFrame := inv.Frame
       if useFrame == "" {
           useFrame = node.FrameNext
       }
       for _, targetID := range targets {
           _ = InvalidateNode(ctx, InvalidateArgs{
               Persist:      args.Persist,
               Logger:       args.Logger,
               TargetNodeID: targetID,
               SourceNodeID: &cand.NodeID,
               Reason:       "handler_invalidate",
               Frame:        useFrame,  // see Task 21 for the InvalidateArgs.Frame addition
           })
       }
   }
   ```
6. The lookup helper `lookupOnAcquireUnavailable` mirrors the existing template lookup in `tryAcquire` (`lookupTemplate` + `lookupNodeDef`).

**Verification:**
```bash
go build ./...
go test ./foundation/integration/... -count=1
```
Both must exit 0. The new code paths only fire when templates declare non-default handlers; existing tests should continue to pass.

---

## Task 16: Apply `on_executor_complete` handler in `runner_terminal.go`

**Files to touch:** `foundation/integration/runner_terminal.go`.

**Steps:**

1. Open `foundation/integration/runner_terminal.go`. Locate `applyTerminalComplete` (~line 64).
2. Before the `UpdateState` call, look up the node's `on_executor_complete` handler from the template (use `acq.NodeDef`). Default to `{Resolve: ResolveByChanged}` when absent.
3. Compute `last_outcome` from the resolution:
   ```go
   var last shared.LastOutcome
   handler := acq.NodeDef.OnExecutorComplete
   resolve := node.ResolveByChanged
   if handler != nil && handler.Resolve != "" {
       resolve = handler.Resolve
   }
   switch resolve {
   case node.ResolveByChanged:
       if t.Changed {
           last = shared.LastOutcomeFreshChanged
       } else {
           last = shared.LastOutcomeFreshUnchanged
       }
   case node.ResolveAlwaysPropagate:
       last = shared.LastOutcomeFreshChanged
   case node.ResolveNeverPropagate:
       last = shared.LastOutcomeFreshUnchanged
   }
   ```
4. Pass `last` into `UpdateState`:
   ```go
   if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
       shared.NodeStateFresh, cascade.ReasonHandlerComplete, last, tx); err != nil {
       return err
   }
   ```
   (The reason changes from `ReasonWorkCompleted` to `ReasonHandlerComplete`. Both transition `running → fresh` per Task 4; the old reason stays as a deprecated alias.)
5. After the tx commits, fire the optional handler.invalidate emit (see emitHandlerInvalidate from Task 15; same helper, different caller):
   ```go
   if handler != nil && handler.Invalidate != nil {
       // Resolve targets in the context of acq (NodeID, InstanceID, NodeType).
       emitHandlerInvalidateForTerminal(ctx, args, acq, handler.Invalidate)
   }
   ```

**Verification:**
```bash
go build ./...
go test ./foundation/integration/... -count=1
```
Must exit 0. Default by_changed behavior preserves today's semantics; existing tests pass.

---

## Task 17: Update cascade gate from `t.Changed` to `last_outcome == fresh_changed`

**Files to touch:** `foundation/integration/runner_terminal.go`.

**Steps:**

1. In `applyTerminalComplete`, locate the cascade-firing gate (line ~108 today: `if t.Changed { cascadeChildrenStaleInTx(...) }`).
2. Change the gate to read the resolved `last_outcome`:
   ```go
   if last == shared.LastOutcomeFreshChanged {
       if err := cascadeChildrenStaleInTx(ctx, args, tx, acq); err != nil {
           return err
       }
   }
   ```
3. Symmetric change at line ~144 (`if t.Changed { fanoutRecalculate(...) }`):
   ```go
   if last == shared.LastOutcomeFreshChanged {
       fanoutRecalculate(ctx, args, acq)
   }
   ```
4. Under default `by_changed` resolution this gate is functionally identical to today's `t.Changed` gate. Under `always_propagate` / `never_propagate` it diverges — add the relevant scenario tests in Task 28 / Task 29.

**Verification:**
```bash
go build ./...
go test ./foundation/integration/... ./test/scenarios/... -count=1
```
Both must exit 0. Today's existing scenarios (which all use the default `by_changed`) should continue to pass.

---

## Task 18: Apply `on_executor_blocked` and `on_executor_errored` handlers

**Files to touch:** `foundation/integration/runner_terminal.go`, `foundation/integration/on_error.go`.

**Steps:**

1. Find `applyTerminalAppError` in `runner_terminal.go` or `on_error.go` (the path that handles Blocked/Errored terminals today).
2. Before the existing error-class routing, look up the relevant handler:
   ```go
   var handler *node.OnExecutorTerminalHandler
   var defaultClass string
   switch terminalKind {
   case "blocked":
       handler = acq.NodeDef.OnExecutorBlocked
       defaultClass = "executor_blocked"
   case "errored":
       handler = acq.NodeDef.OnExecutorErrored
       defaultClass = errorClass // executor-supplied
   }
   ```
3. If `handler != nil && handler.Resolve == ResolvePass`:
   - Call `Abandon` on every `lk` in `acq.Locks` with `lk.Store != nil` (matches `handleOrphanedClaim`).
   - In a fresh tx: UpdateState with `ReasonHandlerPass`, `LastOutcomePassed`.
   - Append a `state_transition` event with `reason=handler_pass`.
   - Fire optional `handler.Invalidate` emit.
   - Skip the error_types policy chain.
4. If `handler != nil && handler.Resolve == ResolveError`:
   - Use `handler.ErrorClass` as the class to route through `error_types`. (If `ErrorClass` is empty, use `defaultClass` — the validator catches missing class for explicit `error` resolution; but defensive fallback is OK.)
   - Call `Abandon` on `acq.Locks` (matches today's `applyTerminalAppError` behavior).
   - Route through `error_types[handler.ErrorClass].policy` per today's mechanism.
   - Fire optional `handler.Invalidate` emit after the policy-chain commit.
5. If `handler == nil`:
   - Today's behavior: route through `error_types[defaultClass].policy`. Preserved unchanged.

**Verification:**
```bash
go build ./...
go test ./foundation/integration/... -count=1 -race
```
Must exit 0.

---

## Task 19: Add `Frame` field to `InvalidateArgs`

**Files to touch:** `foundation/integration/cascade_invalidate.go`.

**Steps:**

1. Open `foundation/integration/cascade_invalidate.go`. Locate `InvalidateArgs` struct definition.
2. Add a new field:
   ```go
   // Frame controls whether the invalidate joins the current cascade
   // (frame: in) or buffers through frame.EnqueueOrCoalesce as a new
   // frame (frame: next; default).
   //
   // Empty string is treated as FrameNext for backwards compatibility.
   //
   // See spec §5.
   Frame string
   ```
3. In `InvalidateNode`, after the audit events:
   ```go
   useFrame := args.Frame
   if useFrame == "" {
       useFrame = node.FrameNext
   }
   if useFrame == node.FrameIn {
       return invalidateInFrame(ctx, args, target)
   }
   // Default next-frame path: today's frame.EnqueueOrCoalesce (preserved unchanged).
   return sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
       fid, err := frame.EnqueueOrCoalesce(ctx, args.Persist, tx, target.InstanceID, target.ID)
       // ... existing logic ...
   })
   ```
4. Implement `invalidateInFrame`:
   ```go
   // invalidateInFrame is the frame: in path. Bypasses
   // frame.EnqueueOrCoalesce and directly transitions the target
   // fresh → stale within the current frame (the source's frame_id).
   //
   // The source must already be in a running frame; if no source
   // frame_id can be determined (e.g., the source is itself stale),
   // fall back to the next-frame path.
   func invalidateInFrame(ctx context.Context, args InvalidateArgs, target *persistence.NodeRow) error {
       if args.SourceNodeID == nil {
           // No source frame to join — fall back to next-frame.
           return invalidateNextFrame(ctx, args, target)
       }
       return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
           src, err := args.Persist.Nodes().Get(ctx, *args.SourceNodeID, tx)
           if err != nil || src == nil || src.FrameID == nil {
               return invalidateNextFrame(ctx, args, target)
           }
           // Mark target stale + frame_id = src.FrameID, in this tx.
           if err := args.Persist.Nodes().MarkStaleForCascade(ctx, target.ID, *src.FrameID, tx); err != nil {
               return err
           }
           return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
               NodeID: &target.ID, InstanceID: &target.InstanceID,
               Kind: "state_transition",
               Payload: map[string]any{
                   "from": "fresh", "to": "stale", "reason": "in_frame_invalidate",
                   "frame_id": src.FrameID.String(),
               },
           }, tx)
       })
   }
   ```
5. Refactor the existing next-frame path into a sibling `invalidateNextFrame` so both paths are explicit.

**Verification:**
```bash
go build ./...
go test ./foundation/integration/... -count=1
```
Both must exit 0.

---

## Task 20: Wire `Frame` through `error_types[X].policy.invalidate`

**Files to touch:** `modeling/node/policy.go`, `foundation/integration/on_error.go`.

**Steps:**

1. In `modeling/node/policy.go`, add a `Frame string` field to `PolicyAction` (new YAML key `frame`):
   ```go
   Frame string `yaml:"frame,omitempty" json:"frame,omitempty"`  // in | next; default next
   ```
2. In `foundation/integration/on_error.go` (or wherever policy actions are applied), when a policy action is `invalidate`, pass the `Frame` field through to `InvalidateNode`:
   ```go
   _ = InvalidateNode(ctx, InvalidateArgs{
       // ... existing fields ...
       Frame: action.Frame,  // empty defaults to FrameNext
   })
   ```
3. Update the validator in `modeling/node/template_validator.go` to validate `PolicyAction.Frame ∈ {"", FrameIn, FrameNext}`.

**Verification:**
```bash
go build ./...
go test ./modeling/node/... ./foundation/integration/... -count=1
```
Must exit 0.

---

## Task 21: Wire `Frame` through `emitHandlerInvalidate`

**Files to touch:** `foundation/integration/runner_acquire.go`, `foundation/integration/runner_terminal.go`.

**Steps:**

1. Confirm both `emitHandlerInvalidate` (acquisition path, Task 15) and `emitHandlerInvalidateForTerminal` (terminal path, Task 16/18) read `Invalidate.Frame` and pass it through to `InvalidateNode` via `InvalidateArgs.Frame`. Default empty → `FrameNext`.

**Verification:**
```bash
go build ./...
```
Must exit 0.

---

## Task 22: Extend `POST /nodes/{id}/invalidate` with `frame` field

**Files to touch:** `modeling/controlapi/nodes.go`.

**Steps:**

1. Open `modeling/controlapi/nodes.go`. Locate the handler for `POST /nodes/{id}/invalidate`.
2. Read the existing request body shape; add a `Frame` field:
   ```go
   type invalidateNodeRequest struct {
       Reason string `json:"reason,omitempty"`
       Frame  string `json:"frame,omitempty"`  // in | next; default next
   }
   ```
3. Validate `Frame ∈ {"", "in", "next"}`. Return 400 on invalid.
4. Pass `Frame` through to `InvalidateNode` via `InvalidateArgs.Frame`.

**Verification:**
```bash
go build ./...
go test ./modeling/controlapi/... -count=1
```
Must exit 0.

---

## Task 23: Add `--frame` flag to `rimsky-cli admin invalidate`

**Files to touch:** `cmd/rimsky-cli/main.go` (or the file implementing admin invalidate).

**Steps:**

1. Locate the admin-invalidate subcommand. Add a `--frame` flag accepting `in` or `next` (default `next`).
2. Pass the value through to the request body's `frame` field.
3. Validation in the CLI (reject other values before sending the request).

**Verification:**
```bash
go build ./...
go test ./cmd/rimsky-cli/... -count=1 || true   # cmd packages may have no tests; build verifies
```
Build must exit 0.

---

## Task 24: Scenario test — `reactive_loop_self_invalidate_next_frame`

**Files to touch:** `test/scenarios/reactive_loop_self_invalidate_next_frame_test.go` (new).

**Steps:**

1. Read existing scenario tests (e.g., `test/scenarios/agentic_executor_async_handoff_test.go`) to understand the harness pattern.
2. Write a test that:
   - Deploys a single-node template with `executor: stub`, a queue-shape claim against a stub claim-producer that yields N=3 items, an `on_executor_complete: { invalidate: { targets: [self], frame: next } }` handler, and an `on_acquire_unavailable: { resolve: pass }` handler.
   - Creates an instance.
   - Drives the scheduler / supervisor harness for a bounded number of ticks (or until `terminated_at` is set, with a hard timeout).
   - Asserts: 3 frames opened, each commits with `last_outcome=fresh_changed`; 4th frame opens with the queue empty; `on_acquire_unavailable: pass` fires; node lands in `fresh+passed`; instance reaches terminal.

**Verification:**
```bash
go test ./test/scenarios/ -run TestReactiveLoopSelfInvalidateNextFrame -count=1
```
Must pass.

---

## Task 25: Scenario test — `reactive_loop_self_invalidate_in_frame`

**Files to touch:** `test/scenarios/reactive_loop_self_invalidate_in_frame_test.go` (new).

**Steps:**

1. Same template shape as Task 24 but with `frame: in` on the invalidate emit.
2. Assert: a single frame stays open for the entire drain; `last_progress_at` updates per iteration; `frame_timeout_ms` warning is not logged.

**Verification:**
```bash
go test ./test/scenarios/ -run TestReactiveLoopSelfInvalidateInFrame -count=1
```
Must pass.

---

## Task 26: Scenario test — `acquire_unavailable_pass`

**Files to touch:** `test/scenarios/acquire_unavailable_pass_test.go` (new).

**Steps:**

1. Single-node template with `on_acquire_unavailable: { resolve: pass }`.
2. Stub producer returns Unavailable on first dispatch.
3. Assert: node transitions to `fresh+passed`; no executor invocation (stub executor's call counter stays 0); no cascade-on-commit fires.

**Verification:**
```bash
go test ./test/scenarios/ -run TestAcquireUnavailablePass -count=1
```
Must pass.

---

## Task 27: Scenario test — `acquire_unavailable_retry_default`

**Files to touch:** `test/scenarios/acquire_unavailable_retry_default_test.go` (new).

**Steps:**

1. Template without any lifecycle handlers.
2. Stub producer returns Unavailable on first dispatch, then Available on second.
3. Assert: silent retry on next scheduler tick (today's behavior); node eventually runs and commits.

**Verification:**
```bash
go test ./test/scenarios/ -run TestAcquireUnavailableRetryDefault -count=1
```
Must pass.

---

## Task 28: Scenario test — `acquire_unavailable_error_routing`

**Files to touch:** `test/scenarios/acquire_unavailable_error_routing_test.go` (new).

**Steps:**

1. Template with `on_acquire_unavailable: { resolve: error, error_class: my_drained }` and `error_types: { my_drained: { policy: [{action: give_up}] } }`.
2. Stub producer returns Unavailable.
3. Assert: error_types policy fires; node lands in `failed`.

**Verification:**
```bash
go test ./test/scenarios/ -run TestAcquireUnavailableErrorRouting -count=1
```
Must pass.

---

## Task 29: Scenario test — `held_claim_acquirer_passes`

**Files to touch:** `test/scenarios/held_claim_acquirer_passes_test.go` (new).

**Steps:**

1. Two-node template: A acquires claim `@q` from a stub queue-producer; B inherits `@q` (B has no other upstream).
2. Producer returns Unavailable. A has `on_acquire_unavailable: { resolve: pass }`.
3. Assert: A passes (`fresh+passed`); B is not woken (today's cascade gate fires only on `fresh_changed`); no `rimsky_claim_handle` rows for the never-acquired claim; held-subgraph auto-terminal does not fire (no held-claim-state to clean up).

**Verification:**
```bash
go test ./test/scenarios/ -run TestHeldClaimAcquirerPasses -count=1
```
Must pass.

---

## Task 30: Scenario test — `held_claim_mixed_upstream`

**Files to touch:** `test/scenarios/held_claim_mixed_upstream_test.go` (new).

**Steps:**

1. Three-node template: A acquires `@q` (passes); C is an independent upstream of B that commits `Changed=true`; B inherits `@q` from A AND depends on C.
2. Assert: C's commit cascades to B; B dispatches; B's substitution into `{{claim.@q.address}}` fails; `template_resolution_failed` routes through B's `error_types[template_resolution_failed].policy = [{give_up}]`; B lands in `failed`.

**Verification:**
```bash
go test ./test/scenarios/ -run TestHeldClaimMixedUpstream -count=1
```
Must pass.

---

## Task 31: Scenario test — `always_propagate_resolution`

**Files to touch:** `test/scenarios/always_propagate_resolution_test.go` (new).

**Steps:**

1. Two-node template A → B. A has `on_executor_complete: { resolve: always_propagate }`.
2. A's executor commits with `changed: false`.
3. Assert: A's `last_outcome=fresh_changed`; cascade-on-commit fires; B is marked stale.

**Verification:**
```bash
go test ./test/scenarios/ -run TestAlwaysPropagateResolution -count=1
```
Must pass.

---

## Task 32: Scenario test — `never_propagate_resolution`

**Files to touch:** `test/scenarios/never_propagate_resolution_test.go` (new).

**Steps:**

1. Two-node template A → B. A has `on_executor_complete: { resolve: never_propagate }`.
2. A's executor commits with `changed: true`.
3. Assert: A's `last_outcome=fresh_unchanged`; cascade-on-commit does NOT fire; B stays fresh.

**Verification:**
```bash
go test ./test/scenarios/ -run TestNeverPropagateResolution -count=1
```
Must pass.

---

## Task 33: Scenario test — `pure_cascade_outcome`

**Files to touch:** `test/scenarios/pure_cascade_outcome_test.go` (new).

**Steps:**

1. Pure-cascade scheduled root with one executor-backed dependent. Cron schedule `* * * * * *` (every second; harness mocks the clock).
2. Trigger one cron fire.
3. Assert: root transitions `stale → fresh, last_outcome=pure_cascade`; cascade-on-commit fires (treating `pure_cascade` as propagating per spec §3.4 table); dependent dispatches.

**Verification:**
```bash
go test ./test/scenarios/ -run TestPureCascadeOutcome -count=1
```
Must pass.

---

## Task 34: Scenario test — `fresh_unchanged_does_not_cascade`

**Files to touch:** `test/scenarios/fresh_unchanged_does_not_cascade_test.go` (new).

**Steps:**

1. Two-node template A → B (no handlers). A's executor commits with `changed: false`.
2. Assert: A's `last_outcome=fresh_unchanged`; cascade-on-commit does NOT fire; B stays fresh. (Today's behavior under default `by_changed`; explicit test that the column-based gate preserves it.)

**Verification:**
```bash
go test ./test/scenarios/ -run TestFreshUnchangedDoesNotCascade -count=1
```
Must pass.

---

## Task 35: Scenario test — `operator_invalidate_target_only`

**Files to touch:** `test/scenarios/operator_invalidate_target_only_test.go` (new).

**Steps:**

1. Three-node chain A → B → C. All fresh.
2. Operator invalidates A via the control-API endpoint.
3. Assert: only A is marked stale (today's behavior preserved); B and C stay fresh; cascade happens lazily as A's commit propagates.

**Verification:**
```bash
go test ./test/scenarios/ -run TestOperatorInvalidateTargetOnly -count=1
```
Must pass.

---

## Task 36: Scenario test — `failed_upstream_freezes_downstream`

**Files to touch:** `test/scenarios/failed_upstream_freezes_downstream_test.go` (new).

**Steps:**

1. Two-node template A → B. A's policy resolves `give_up` on its first run.
2. Assert: A lands in `failed, last_outcome=failed`; B stays in its previous state (today's behavior; failures freeze downstream).

**Verification:**
```bash
go test ./test/scenarios/ -run TestFailedUpstreamFreezesDownstream -count=1
```
Must pass.

---

## Task 37: Scenario test — `executor_blocked_pass_resolution`

**Files to touch:** `test/scenarios/executor_blocked_pass_resolution_test.go` (new).

**Steps:**

1. Single-node template with `on_executor_blocked: { resolve: pass }`. Stub executor returns Blocked.
2. Assert: node lands in `fresh+passed`; no error_types routing; producer-side `Abandon` was called for any acquired claims (stub producer's Abandon counter incremented).

**Verification:**
```bash
go test ./test/scenarios/ -run TestExecutorBlockedPassResolution -count=1
```
Must pass.

---

## Task 38: Scenario test — `executor_errored_pass_resolution`

**Files to touch:** `test/scenarios/executor_errored_pass_resolution_test.go` (new).

**Steps:**

1. Single-node template with `on_executor_errored: { resolve: pass }`. Stub executor returns Errored.
2. Assert: node lands in `fresh+passed`; no error_types routing; Abandon called.

**Verification:**
```bash
go test ./test/scenarios/ -run TestExecutorErroredPassResolution -count=1
```
Must pass.

---

## Task 39: Scenario test — `frame_coalesce_self_invalidate`

**Files to touch:** `test/scenarios/frame_coalesce_self_invalidate_test.go` (new).

**Steps:**

1. Single-node template with `on_executor_complete: { invalidate: { targets: [self], frame: next } }` and `frame_resolution: coalesce`.
2. Drive multiple rapid commits.
3. Assert: pending self-invalidates collapse to one pending frame; no double-execute (verify by running with `-race -count=5`).

**Verification:**
```bash
go test ./test/scenarios/ -run TestFrameCoalesceSelfInvalidate -count=5 -race
```
Must pass under race-mode.

---

## Task 40: Scenario test — `frame_timeout_progressing_loop`

**Files to touch:** `test/scenarios/frame_timeout_progressing_loop_test.go` (new).

**Steps:**

1. Single-node template with `on_executor_complete: { invalidate: { targets: [self], frame: in } }` and `frame_timeout_ms: 5000`.
2. Drive a self-invalidate loop where each iteration takes ~1 second; total runtime exceeds `frame_timeout_ms`.
3. Assert: `last_progress_at` updates per iteration; the soft warning is not logged.

**Verification:**
```bash
go test ./test/scenarios/ -run TestFrameTimeoutProgressingLoop -count=1
```
Must pass.

---

## Task 41: Scenario test — `frame_timeout_stuck_frame`

**Files to touch:** `test/scenarios/frame_timeout_stuck_frame_test.go` (new).

**Steps:**

1. Set up a frame with one stuck node (e.g., executor never returns; harness mocks the clock).
2. Advance the mocked clock past `frame_timeout_ms`.
3. Assert: the soft warning IS logged (capture via test logger).

**Verification:**
```bash
go test ./test/scenarios/ -run TestFrameTimeoutStuckFrame -count=1
```
Must pass.

---

## Task 42: Scenario test — `handler_invalidate_orthogonal_to_changed`

**Files to touch:** `test/scenarios/handler_invalidate_orthogonal_to_changed_test.go` (new).

**Steps:**

1. Two-node template: `worker` has `on_executor_complete: { resolve: by_changed, invalidate: { targets: [monitor], frame: next } }`. `monitor` is a separate node with no upstream.
2. `worker`'s executor commits with `changed: false`.
3. Assert: `worker.last_outcome = fresh_unchanged`; cascade-on-commit does NOT fire to dependents of worker; BUT `monitor` is invalidated (received the handler.invalidate emit).

**Verification:**
```bash
go test ./test/scenarios/ -run TestHandlerInvalidateOrthogonalToChanged -count=1
```
Must pass.

---

## Task 43: Scenario test — `acquire_pass_invalidate_emit`

**Files to touch:** `test/scenarios/acquire_pass_invalidate_emit_test.go` (new).

**Steps:**

1. Two-node template: `worker` with `on_acquire_unavailable: { resolve: pass, invalidate: { targets: [monitor], frame: next } }`. `monitor` is a separate node.
2. Producer returns Unavailable.
3. Assert: `worker` passes; executor not invoked; `monitor` is invalidated (received the handler.invalidate emit even though no executor ran).

**Verification:**
```bash
go test ./test/scenarios/ -run TestAcquirePassInvalidateEmit -count=1
```
Must pass.

---

## Task 44: Update `docs/concepts/node-state.md`

**Files to touch:** `docs/concepts/node-state.md`.

**Steps:**

1. Read the existing file.
2. Add a new section under "How you encounter it" or "Anatomy" describing the `last_outcome` field:
   - The five flavor values.
   - That `last_outcome` is observability metadata, not a dispatch gate.
   - That it's written at every transition that lands a terminal-for-this-frame state.
   - Reference the spec by path.
3. Update the existing state-table description to note that the four states are unchanged.

**Verification:**
```bash
make docs-lint || true   # optional; run if a docs-lint target exists
```
No blocking verification; manual review during reviewer pass.

---

## Task 45: Update `docs/concepts/node.md`

**Files to touch:** `docs/concepts/node.md`.

**Steps:**

1. Read the existing file.
2. Add a "Lifecycle handlers" subsection describing the four declarable slots, the per-handler `resolve:` vocabulary, the `invalidate:` slot, and the orthogonality rule. Cross-reference the spec.
3. Update the example node-spec YAML if the doc carries one.

**Verification:**
Reviewer pass.

---

## Task 46: Update `docs/concepts/cascade.md`

**Files to touch:** `docs/concepts/cascade.md`.

**Steps:**

1. Read the existing file.
2. Add a note that today's lazy + Changed-gated cascade is preserved end-to-end. Reference the spec's §6 explicitly.
3. Add a paragraph clarifying that `last_outcome` is observability metadata, not a dispatch gate. The cascade-firing gate (which controls `cascadeChildrenStaleInTx` and `fanoutRecalculate`) is now expressed as `last_outcome == fresh_changed`, functionally identical to today's `t.Changed` under the default `by_changed` handler.

**Verification:**
Reviewer pass.

---

## Task 47: Update `docs/concepts/frame.md`

**Files to touch:** `docs/concepts/frame.md`.

**Steps:**

1. Read the existing file.
2. Update the description of `frame_timeout_ms` to reflect the new "no progress in window" semantics. Note that progress = any node state transition in the frame.
3. Add a brief comparison: this is distinct from per-run executor silence-timeout (which lives on the executor peer).

**Verification:**
Reviewer pass.

---

## Task 48: Update `docs/concepts/invalidate.md`

**Files to touch:** `docs/concepts/invalidate.md`.

**Steps:**

1. Read the existing file.
2. Document the per-emit `frame: in | next` field, including:
   - Where it can appear (operator API; error_types policy invalidate; lifecycle-handler invalidate).
   - Defaults (next-frame everywhere except cascade recalculate, which is a scheduler action and not configurable).
3. Reinforce the existing "Common mistakes" entry: recalculation is a scheduler action, not a peer message; only `invalidate` is a graph-level message.

**Verification:**
Reviewer pass.

---

## Task 49: Update `docs/specs/2026-05-04-foundation-contract.md`

**Files to touch:** `docs/specs/2026-05-04-foundation-contract.md`.

**Steps:**

1. Read the existing contract spec.
2. Add a brief note that the foundation layer's supervisor terminal handler now dispatches lifecycle-handler resolutions (per the new spec at `.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`).
3. Note that `last_outcome` is a foundation-layer column on `rimsky_nodes`.

**Verification:**
Reviewer pass.

---

## Task 50: Update `docs/specs/2026-05-04-modeling-layer-contract.md`

**Files to touch:** `docs/specs/2026-05-04-modeling-layer-contract.md`.

**Steps:**

1. Read the existing contract spec.
2. Add a brief note that the modeling layer's template-spec gains the four lifecycle-handler blocks (validated at template-deploy).
3. Note the per-emit `frame:` field on PolicyAction.

**Verification:**
Reviewer pass.

---

## Task 51: Update `CLAUDE.md`

**Files to touch:** `CLAUDE.md`.

**Steps:**

1. The "Vocabulary" line should mention `last_outcome` as a sibling field to `state`. Suggested wording:
   > Vocabulary: 1 graph-level message (`invalidate`); 4 node states (`fresh`, `stale`, `running`, `failed`) plus a sibling `last_outcome` column for resolution flavor (`fresh_changed`, `fresh_unchanged`, `passed`, `pure_cascade`, `failed`); 4 declarable lifecycle handlers (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`); 3 error actions (`retry`, `invalidate(targets)`, `give_up`).
2. Add a paragraph under "Non-obvious gotchas" capturing:
   - The cascade-firing gate moved from `t.Changed` to `last_outcome == fresh_changed` (functionally identical under default `by_changed`).
   - `pass` and `error` resolutions on `on_acquire_unavailable`/`on_executor_blocked`/`on_executor_errored` call `Abandon` on already-Open'd claims (matching `handleOrphanedClaim`).
   - The new `frame: in | next` field on invalidate emits.

**Verification:**
Reviewer pass.

---

## Task 52: Update `CHANGELOG.md`

**Files to touch:** `CHANGELOG.md`.

**Steps:**

1. Add a new entry under `## Unreleased`:

   ```markdown
   ### Reactive loops + lifecycle handlers

   Per `.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`. Adds four declarable lifecycle-handler slots on each node (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`); a `last_outcome` column on `rimsky_nodes` capturing the resolution flavor (`fresh_changed | fresh_unchanged | passed | pure_cascade | failed`); a per-emit `frame: in | next` field on every invalidate emit declaration; and a `last_progress_at`-based refinement of `frame_timeout_ms` semantics ("no progress in window" instead of frame age).

   - **Templates without lifecycle-handler blocks are unaffected.** Defaults preserve today's hardcoded supervisor behavior.
   - **The cascade-firing gate is now `last_outcome == fresh_changed`** instead of `t.Changed` directly. Functionally identical under the default `by_changed` resolution; diverges under explicit `always_propagate` / `never_propagate`.
   - **`pass` and `error` resolutions on `on_acquire_unavailable` / `on_executor_blocked` / `on_executor_errored` call `Abandon`** on already-Open'd claims, matching `handleOrphanedClaim` semantics.
   - **`frame_timeout_ms` measures "no progress in window"** via the new `rimsky_frames.last_progress_at` column. Soft warning behavior preserved; no teeth.
   - **New TransitionReason kinds:** `acquire_pass`, `handler_complete` (subsumes `work_completed` for new code paths; old name kept as deprecated alias for one cycle), `handler_error` (audit-log only; not a direct NextState input), `handler_pass`.
   - **Schema:** new ALTER TABLE migrations adding `last_outcome TEXT` to `rimsky_nodes` and `last_progress_at TIMESTAMPTZ NOT NULL DEFAULT now()` to `rimsky_frames`. Pre-v1; no compat shim. Existing dev DBs accept the new columns via the migration runner.
   ```

**Verification:**
Reviewer pass.

---

## Task 53: Update cold-read annotations

**Files to touch:** `foundation/cascade/state.go`, `foundation/persistence/postgres/nodes.go`, `foundation/persistence/sqlite/nodes.go`, any other annotated files modified above.

**Steps:**

1. Run `grep -rn "@blessed-invariant\|@source\|@diverged\|@agent-contract" foundation/ modeling/ | grep -v _test.go` to enumerate annotated locations.
2. For each annotated file modified by this plan, ensure annotations are still accurate. In particular:
   - `foundation/cascade/state.go` — the `@blessed-invariant (§17)` annotation on `NextState` must stay; the new transitions are added without weakening the invariant.
   - `foundation/persistence/postgres/nodes.go::enforceAndUpdate` — the `@blessed-invariant (§17)` annotation on the `cascade.NextState` call site must stay.
3. Add new annotations where appropriate:
   - On `acquireClaim` / `acquireOneLock`'s new return type, document the `openResult` discriminator briefly.
   - On `applyAcquirePass` / `applyAcquireError` / `handleAcquireUnavailable`, cross-reference the spec.

**Verification:**
```bash
make lint
```
Must exit 0.

---

## Task 54: Run full Go test suite + race-mode

**Files to touch:** none (verification only).

**Steps:**

1. Run the full test suite from the repo root:
   ```bash
   go test ./... -count=1
   ```
2. Run race-mode on the supervisor / scheduler / queue / persistence paths per `rules.md`:
   ```bash
   go test ./foundation/integration/... ./modeling/scheduler/... ./foundation/persistence/... -race -count=3
   ```
3. Run lint:
   ```bash
   make lint
   ```

**Verification:**
All three commands exit 0.

---

## Task 55: Run conformance suite

**Files to touch:** none (verification only).

**Steps:**

1. Build the conformance binary:
   ```bash
   go build -o bin/rimsky-conformance ./cmd/rimsky-conformance
   go build -o bin/rimsky-conformance-probe ./cmd/rimsky-conformance-probe
   ```
2. Bring up the docker-compose stack with executors in stub mode (per the existing pattern in `deploy/docker-compose.yml`):
   ```bash
   docker compose -f deploy/docker-compose.yml up -d
   ```
3. Wait for `/health` to respond, then run conformance against the executors:
   ```bash
   go run ./cmd/rimsky-conformance --endpoint claude-agent:9090 --transport grpc --require-stub-mode
   go run ./cmd/rimsky-conformance --endpoint http-node:9091 --transport grpc --require-stub-mode
   ```
4. Tear down:
   ```bash
   docker compose -f deploy/docker-compose.yml down -v
   ```

**Verification:**
Both conformance runs exit 0. (If conformance isn't relevant — i.e., none of the changes touch the executor protocol — note that and skip; per the spec, this work doesn't change the executor protocol surface.)

---

## Task 56: Run TypeScript executor tests

**Files to touch:** none (verification only).

**Steps:**

1. From the repo root:
   ```bash
   cd executors/claude-agent
   npm install
   npm test
   npm run build
   cd -
   ```

**Verification:**
All commands exit 0.

---

## Task 57: Implementation notes

**Files to touch:** `.ok-planner/plans/2026-05-05-reactive-loops-and-lifecycle-handlers-notes.md` (new).

**Steps:**

1. Create an implementation notes file capturing:
   - Any deviations from the plan (and why).
   - Specific decisions made on the spec's §13 open questions:
     - Final names chosen for the new `TransitionReason` kinds (if different from the indicative names in this plan).
     - Whether `ReasonWorkCompleted` was renamed in place or kept as an alias.
     - Whether `UpdateState`'s signature changed (this plan's Task 9 picks "extended signature"; if the implementer chose a sibling method instead, document that).
   - Any flake-hunting or race-condition findings during `-race -count=3` runs.
   - Any issues encountered with the docker-compose conformance run.
2. This file is for the user's review; the plan-archive step (run by the execute-* skill) moves the spec and plan into history but the notes stay attached for one cycle.

**Verification:** none — this is a working artifact, not a code change.

---

## Manual checks after completion

These items require human review and are not part of the automated run.

- **Visual review of the dashboard's `last_outcome` rendering.** The dashboard at `/dashboards/rimsky-dashboard/` may need updates to surface the new flavor; this plan does not touch the dashboard. If the dashboard team wants `last_outcome` rendered, a follow-up issue covers that.
- **Operator-runbook spot-check.** Read the updated `docs/concepts/{node-state, node, cascade, frame, invalidate}.md` files for clarity. If the prose is confusing, file a docs-cleanup follow-up.
- **End-to-end sanity check against the docs-pipeline driving consumer.** The verantel docs-pipeline sketch is downstream; once this plan lands, the verantel sketch can be reified into a template that exercises the new primitives. That work is out of scope here but is the realistic next consumer to verify the design at a system level.
