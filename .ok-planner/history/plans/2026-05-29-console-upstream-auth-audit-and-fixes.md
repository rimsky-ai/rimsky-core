# Console-upstream: auth model, audit durability, action fixes — Implementation Plan

**Spec:** .ok-planner/specs/2026-05-29-console-upstream-auth-audit-and-fixes-design.md
**Goal:** Make dry-run a per-request flag and permission binary; make the event log durable; add an audit read surface; fix the prune-count and backfill-validation bugs; complete the serial_queue trigger-message body — and apply the matching design-doc mutations.
**Architecture:** Control-plane (`lib/control/controlapi`) auth middleware + handlers, persistence interfaces + postgres/sqlite drivers + conformance (`lib/foundation/persistence`), the dispatch substitution context (`lib/runtime` + `lib/graph/attribute`), the CLI (`cmd/rimsky/cli`), and the design catalog (`.ok-planner/design`).
**Tech Stack:** Go; `go-chi/chi` routing; `jackc/pgx/v5` (Postgres) + `modernc.org/sqlite` (pure-Go); stdlib `log/slog`.

> Source of truth is the spec. This plan is operational (which file, what edit, what verification). It executes end-to-end in one `execute-plan` run. Standard project gates apply at each `working` pass: `go build ./... && go test ./... && make lint`; scenario/persistence tests use testcontainers (Docker must be up).

> **Load-bearing properties this plan makes explicit (do not trade away):**
> - **Dry-run never mutates** (Pass 2): every write action returns its `would_have_*` envelope *before any mutation*; enforced by a coverage conformance test, not happy-path checks.
> - **Audit durability** (Pass 3): the `auth.access_attempted` write is **synchronous in the request goroutine**; do NOT reintroduce an async/buffered/droppable path. Verified under concurrent load.
> - **Backfill reject, not silent-degrade** (Pass 6): a non-fan-out target is refused at submit (400), never accepted-and-ignored.
> - **Backfill overrides reach the node** (Pass 7): the fan-out `partition_request` is substituted from the triggering message at acquisition — the regression test asserts the override changes the partitions processed, not just that it ran.
> - **No silent override loss under coalesce** (Pass 8): two messages that would bind a node's substitution to different values land in separate frames; only same-value bindings coalesce. (Default delivery flips to `serial_queue`.)

---

## Pass 1: Permission becomes binary + per-request dry-run flag

**Goal:** Drop the per-grant `mode` modifier; permission becomes set-membership; the request mode comes from a `?dry_run=true` query param. Keep the build green (this is compile-coupled with the CLI).
**Scope:** Tasks 1–4
**End state:** working
**Verification:** `go build ./... && go test ./lib/foundation/auth/... ./lib/control/controlapi/... ./cmd/rimsky/... && make lint`

### Task 1: Drop `GrantEntry.Mode` and simplify the evaluator to set-membership

**Files:** `lib/foundation/auth/grant.go`, `lib/foundation/auth/check.go`, `lib/foundation/auth/grant_test.go`, `lib/foundation/auth/check_test.go`

Current (`grant.go:15-42`):
```go
type Mode string
const (
	ModeExecute Mode = "execute"
	ModeDryRun  Mode = "dry_run"
)
type GrantEntry struct {
	Action string `json:"action"`
	Mode   Mode   `json:"mode,omitempty"`
	Extras map[string]json.RawMessage `json:"-"`
}
```
`CheckGrant` lives in `check.go` (`check.go:23`) and returns `CheckResult` (`check.go:10`), which carries `.Mode` (`check.go:12`).

**Steps:**
1. In `grant.go`: remove the `Mode` field from `GrantEntry` (keep `Action`, `Extras`). **Keep** the `Mode` type and the `ModeExecute`/`ModeDryRun` constants — they remain the *request-mode* vocabulary used by `ModeFromContext`, `WriteDryRunResponse`, and the dry-run handler branches. Also edit `GrantEntry.UnmarshalJSON` (`grant.go:~78`) and `MarshalJSON` (`grant.go:~110-111`) to drop the `Mode` handling — an incoming `mode` key now falls into `Extras` like any other unknown field.
2. In `check.go`: remove the `.Mode` field from `CheckResult` and the first-match-wins mode-resolution logic in `CheckGrant`. `CheckGrant` now answers **allowed/denied only** — allowed iff any entry's action matches (wildcard rules unchanged).
3. Rewrite the tests that assert the old shape so the package compiles and passes: `check_test.go`'s `TestCheckGrantFirstMatchWins_*` (rewrite to set-membership allow/deny assertions; drop `res.Mode`) and `grant_test.go`'s round-trip/array tests (drop `GrantEntry{Mode: ...}` literals and `Mode` assertions).
4. `go build ./lib/foundation/auth/... && go test ./lib/foundation/auth/...` green.

**Verification:** `go test ./lib/foundation/auth/... -count=1` passes; `CheckResult` and `GrantEntry` have no `Mode` field.

### Task 2: Resolve request mode from `?dry_run=true`; plumb `IsWrite` into the audit emit

**Files:** `lib/control/controlapi/auth_middleware.go`, `lib/control/controlapi/auth.go`

Current mode resolution (`auth_middleware.go:340-344`):
```go
ctx := context.WithValue(r.Context(), ctxKeyMode{}, res.Mode)
```

**Steps:**
1. Replace the `res.Mode` source with the request flag. After the allow check, compute:
   ```go
   mode := auth.ModeExecute
   if r.URL.Query().Get("dry_run") == "true" {
       mode = auth.ModeDryRun
   }
   ctx := context.WithValue(r.Context(), ctxKeyMode{}, mode)
   ```
   (`ModeFromContext` in `auth.go:55-60` is unchanged — it still reads `ctxKeyMode`.)
2. `executed` semantics: the audit emit currently computes `Executed: mode == auth.ModeExecute && status < 400`. A dry-run'd **read** ran, so `executed` must be `true` for reads. The gate has the `action` string; look up `IsWrite` via `s.Registry.Entry(action)` (`actions.go:134`, returns `(ActionEntry, bool)`; `ActionEntry.IsWrite` at `actions.go:31`) and pass it into `emitAttempted`. Change the `Executed` computation to:
   ```go
   // isWrite from Registry.Entry(action); reads always "execute" their read
   executed := status < 400 && (!isWrite || mode == auth.ModeExecute)
   ```
   Thread `isWrite` (or the resolved `ActionEntry`) into `emitAttempted`'s signature and update its body + call site.
3. The audit row's `mode` field continues to record the resolved request mode (now from the flag).

**Verification:** `go build ./lib/control/controlapi/...`; a unit test (or existing auth test) showing `?dry_run=true` sets `ModeFromContext` to `ModeDryRun` and a read records `executed:true`.

### Task 3: Remove the CLI `--dry-run` grant flag and the `Mode` literals

**Files:** `cmd/rimsky/cli/auth_create.go`, `cmd/rimsky/cli/auth_common.go`, `cmd/rimsky/cli/auth_common_test.go`

Current (`auth_common.go:117,140`): `applyGrantPatches` appends `auth.GrantEntry{Action: a, Mode: auth.ModeExecute}` (`--add`) and `{Action: a, Mode: auth.ModeDryRun}` (`--dry-run`). `auth_create.go:44` wires the `--dry-run` flag. `auth_common_test.go:68` asserts `got[1].Mode == auth.ModeDryRun`.

**Steps:**
1. Remove the `--dry-run=<action>` grant flag from `auth_create.go` (and any other key-mutation command that registers it — grep `dry-run` under `cmd/rimsky/cli/`). Per-grant dry-run no longer exists.
2. In `applyGrantPatches` (`auth_common.go`), remove the `dryRun []string` parameter and its patch branch entirely, and strip the `Mode:` field from the `--add` branch (entries are now `{Action: a}`). Update the sole caller at `auth_create.go:62` to drop the `dryRun` argument.
3. Update `auth_common_test.go`: delete the dry-run-rejection cases `TestApplyGrantPatches_DryRunReadRejected` (`:73`) and `TestApplyGrantPatches_DryRunAuthRejected` (`:80`), and the `Mode`-asserting case (`:68`); assert the remaining patched entries carry only `Action`.

**Verification:** `go build ./cmd/rimsky/... && go test ./cmd/rimsky/cli/...`.

### Task 4: Drop persisted-grant `mode` tolerance note / dead references

**Files:** grep-driven — `rg -n 'ModeDryRun|ModeExecute|\.Mode\b' lib cmd --glob '!**/gen/**'`

**Steps:**
1. Run the grep. For each remaining reference to a *grant-entry* `.Mode` (not the request mode in the middleware/handlers/`dryrun.go`), remove or fix it. Expected remaining legitimate uses, all of which **stay**: `auth.ModeExecute`/`auth.ModeDryRun` as the *request* mode (middleware, `ModeFromContext`, `WriteDryRunResponse`, dry-run handler branches); and the unrelated breakpoint `.Mode` (`breakpoints.go:93,137` — the breakpoint pause/notify mode, nothing to do with auth). Touch only grant-entry/`CheckResult` `.Mode`.
2. Pre-v1: no migration for existing persisted grants carrying `mode` — the field is simply ignored on decode (preserved in `Extras` if present). Confirm the grant JSON decoder tolerates an unknown `mode` key (it does, via `Extras`); no further action.

**Verification:** `go build ./... && make lint` (full build green; this is the pass gate).

---

## Pass 2: Dry-run write coverage + read no-op + coverage conformance test

**Goal:** Every write action has a dry-run branch (no carve-outs); reads are no-op previews; a conformance test enforces it.
**Scope:** Tasks 5–9
**End state:** working
**Verification:** `go test ./lib/control/controlapi/... ./test/scenarios/auth/... -count=1`

> **Load-bearing property:** a request in `dry_run` mode performs **no mutation**. Each branch must return its `would_have_*` envelope before any state change. The Task 9 conformance test is the guarantee — it must drive every write action, not a sample.

### Task 5: Add dry-run branches to `instance:pause` / `instance:resume`

**Files:** `lib/control/controlapi/instances.go` (handlers around lines 199-256)

Pattern to mirror (`instances.go:653-657`):
```go
if WriteDryRunResponse(w, req, "would_have_terminated", map[string]any{
	"instance_id": inst.ID.String(),
}) { return }
```

**Steps:**
1. In the pause handler, after request/instance validation and **before** the pause mutation, add `if WriteDryRunResponse(w, req, "would_have_paused", map[string]any{"instance_id": <resolved-instance>.ID.String()}) { return }` (use whatever the handler names the resolved instance — `inst.ID` in the kill/terminate handlers).
2. Same for resume with `"would_have_resumed"`.

**Verification:** `go test ./lib/control/controlapi/... -run Pause` (add/extend a test asserting `?dry_run=true` returns the envelope and does not flip the paused flag).

### Task 6: Add dry-run branches to the breakpoint writes

**Files:** `lib/control/controlapi/breakpoints.go`

**Steps:**
1. `breakpoint:create` — after validation (instance resolve, body parse, checkpoint/matcher validation), before insert: `WriteDryRunResponse(w, req, "would_have_created_breakpoint", {instance_id, matcher/checkpoint summary})`.
2. `breakpoint:resume` — before the resume mutation: `"would_have_resumed_breakpoint"` with `{hit_id}`.
3. `breakpoint:delete` — before delete: `"would_have_deleted_breakpoint"` with `{breakpoint_id}`.

**Verification:** `go test ./lib/control/controlapi/... -run Breakpoint` (dry-run returns envelope; ledger unchanged).

### Task 7: Add dry-run branches to the auth mutations; remove the carve-out

**Files:** `lib/control/controlapi/auth_handlers.go`

Current carve-out (`auth_handlers.go:65-70`, comment on `handleCreateKey`): "auth mutations are NOT dry-runnable in V1 … the handler intentionally ignores ModeFromContext."

**Steps:**
1. Remove that comment block.
2. `auth:create` (`handleCreateKey`) — after grant validation, before minting: in dry-run, return a placeholder and mint **no** plaintext:
   ```go
   if ModeFromContext(r.Context()) == auth.ModeDryRun {
       details := map[string]any{"key_id": "dry-run-not-persisted", "name": body.Name, "permissions": body.Permissions}
       if isAnon, _ := deps.AuthState.IsAnonymousMode(r.Context()); isAnon {
           details["note"] = "this is the first key; committing it exits anonymous mode and requires auth on all future requests"
       }
       WriteDryRunResponseForced(w, "would_have_created_key", details)
       return
   }
   ```
   (`IsAnonymousMode` is a method on `*AuthState`, `auth_middleware.go:108-120`, reached as `deps.AuthState.IsAnonymousMode(ctx) (bool, error)` — there is no `deps.Auth` field.)
3. `auth:revoke` — before setting the revocation timestamp: `WriteDryRunResponse(w, req, "would_have_revoked_key", {key_id})`.
4. `auth:rotate` — before the rotate transaction: `WriteDryRunResponse(w, req, "would_have_rotated_key", {key_id})`.

**Verification:** `go test ./lib/control/controlapi/... -run Auth` and `./test/scenarios/auth/... -run DryRun` (auth mutations under `?dry_run=true` mutate nothing; `auth:create` returns placeholder + no plaintext; anonymous-mode create carries the note).

### Task 8: Confirm read no-op behavior

**Files:** test-only (`lib/control/controlapi/...` or `test/scenarios/auth/...`)

> Reads need no handler change — a read handler ignores mode and runs; the `executed:true` accounting landed in Task 2.

**Steps:**
1. Add a test: a `*:read` action invoked with `?dry_run=true` returns the normal read body and the audit row has `mode:dry_run, executed:true`.

**Verification:** the new test passes.

### Task 9: Dry-run coverage conformance test (the structural guarantee)

**Files:** new test, e.g. `test/scenarios/auth/dry_run_coverage_test.go` (co-locate with the existing `dry_run_test.go`)

**Steps:**
1. Enumerate every write action from the registry: iterate `BuildV1Registry()` entries where `IsWrite == true`.
2. For each, drive a representative request (use each action's primary `Route`) with `?dry_run=true` against a real stack (testcontainers, as the other `test/scenarios/auth` tests do).
3. Assert for each: (a) the response carries `dry_run: true` with a `would_have_*` key, and (b) no mutation occurred (the action's target state is unchanged — e.g. re-fetch the instance/key/ledger and assert no change).
4. The test fails if any write action either mutates under dry-run or lacks the envelope — this is what guarantees "forced dry-run never mutates" without a runtime gate.

**Verification:** `go test ./test/scenarios/auth/... -run DryRunCoverage -count=1` passes and visibly covers every `IsWrite` action (log the count).

---

## Pass 3: Event-log durability (synchronous audit write)

**Goal:** Replace the async/droppable audit dispatcher with a synchronous in-request write.
**Scope:** Tasks 10–11
**End state:** working
**Verification:** `go build ./... && go test ./lib/control/controlapi/... ./test/scenarios/auth/... -run Audit -count=1`

> **Load-bearing property:** the `auth.access_attempted` row is written **synchronously in the request goroutine, after the handler returns** (so `response_status`/`duration_ms` are known) and before the gate returns. Do NOT reintroduce a queue/worker/buffer. The Task 11 test drives concurrent load and asserts zero drops.

### Task 10: Delete the dispatcher; write synchronously

**Files:** `lib/control/controlapi/audit.go`, `lib/control/controlapi/auth_middleware.go`, `lib/control/config/controlapi.go`

Current: `auditDispatcher` (`audit.go`, bounded queue + workers, `submit` drops on full); `AuthState.auditDisp` field (`auth_middleware.go:72`) + `EnsureAuditDispatcher` (`:146`) / `StopAuditDispatcher` (`:170`) / `dispatcher()` (`:160`); call sites `controlapi.go:258` (start) and `:142` (stop).

**Steps:**
1. In `audit.go`, delete the `auditDispatcher` type, its queue/worker code, and `submit`. Keep `insertEvent` (the marshal + `Tables.Transaction` + `Events().Append` bridge).
2. In `auth_middleware.go`, remove the `auditDisp` field and the `EnsureAuditDispatcher`/`StopAuditDispatcher`/`dispatcher()` methods. Change `emitAttempted`/`emitDenied` to call `insertEvent` directly (synchronously) instead of enqueuing.
3. In `controlapi.go`, remove the `EnsureAuditDispatcher()` startup call (`:258`) and the `StopAuditDispatcher()` shutdown call (`:142`).
4. Confirm `emitAttempted` is invoked after the handler in `gateByAction` (it is, `auth_middleware.go:347-349`) — ordering already correct. `emitDenied` on the 401/403 paths stays synchronous (already is).

**Verification:** `go build ./... && make lint`; grep confirms no `auditDispatcher`/`submit`/`EnsureAuditDispatcher` references remain.

### Task 11: Durability test under load

**Files:** new test in `test/scenarios/auth/` (or extend `lifecycle_test.go`)

**Steps:**
1. Fire N concurrent authenticated requests against a real stack; after they return, query the event log and assert exactly N `auth.access_attempted` rows (no drops). N large enough to have overflowed the old queue (e.g. ≥ the old `auditQueueSize`).

**Verification:** `go test ./test/scenarios/auth/... -run AuditDurability -count=1`.

---

## Pass 4: Audit read surface (`audit:read` + `GET /audit`)

**Goal:** A filterable read over the `auth.*` rows, gated by a new action, backed by expression indexes.
**Scope:** Tasks 12–15
**End state:** working
**Verification:** `go test ./lib/foundation/persistence/... ./lib/control/controlapi/... ./test/scenarios/... -run 'Audit|Event' -count=1`

### Task 12: Add the `audit:read` action + role-template coverage

**Files:** `lib/control/controlapi/actions.go`, `cmd/rimsky/cli/roles/*.json`

Mirror `event:read` (`actions.go:320-323`):
```go
{Action: "audit:read", IsWrite: false,
	Routes:      []Route{{"GET", "/audit"}},
	MCPTools:    []string{"audit_list"},
	Description: "Read the auth audit log."},
```

**Steps:**
1. Add the entry to `v1Actions` in `BuildV1Registry`.
2. Role coverage: `admin` (`*`) and `read-only` (`*:read`) cover `audit:read` via wildcards — verify. Add an explicit `{"action":"audit:read"}` to `cmd/rimsky/cli/roles/operator.json` (operator is not a blanket `*:read`). Verify the other role JSONs.

**Verification:** `go test ./lib/control/controlapi/... -run Registry`; a test that a `read-only` key resolves `audit:read` and an `operator` key does too.

### Task 13: Extend `EventListFilter` with auth-payload fields (both drivers + conformance)

**Files:** `lib/foundation/persistence/events.go`, `.../postgres/events.go`, `.../sqlite/events.go`, `.../conformance/observability.go`

Current filter (`events.go:34-43`): `InstanceID/NodeID/Kind/KindIn/Since/Until`.

**Steps:**
1. Add fields to `EventListFilter`: `KeyID *string`, `KeyName *string`, `ActionExact *string`, `ActionPrefix *string`, `ResponseStatus *int`, `Mode *string`. (Document that these filter the JSONB `payload` and are meaningful for `auth.*` kinds.)
2. Postgres `List` (`postgres/events.go:72-88`): extend the WHERE clause with `($N::text IS NULL OR payload->>'key_id' = $N)` etc., and `payload->>'action' LIKE $N || '%'` for the prefix case. Add the params.
3. SQLite `List` (`sqlite/events.go:98-110`): same predicates via `json_extract(payload,'$.key_id')` etc.
4. Conformance (`conformance/observability.go`): add a test mirroring `testEventsListDescending` (`conformance/observability.go:141`) that inserts `auth.access_attempted` rows with varied `key_id`/`action`/`response_status`/`mode` payloads and asserts each filter narrows correctly. **Wire it via a `t.Run(...)` entry in `conformance/conformance.go`** so it actually runs (both drivers run the suite).

**Verification:** `go test ./lib/foundation/persistence/... -run 'EventsList|Conformance' -count=1` (postgres via testcontainers + sqlite).

### Task 14: Add the `GET /audit` route + handler

**Files:** new `lib/control/controlapi/audit_read.go` (route + handler); register it where `registerEventsRoutes` is registered

Mirror `events.go:31-32`:
```go
func registerAuditRoutes(r chi.Router, deps AppDeps) {
	r.Get("/audit", gate(deps, "audit:read", handleListAudit(deps)))
}
```

**Steps:**
1. `handleListAudit` parses query params (actor `key_id`/`key_name`, `action` exact-or-prefix, `target` = request_path, `since`/`until`, `status`, `mode`, cursor) into an `EventListFilter` with `KindIn: ["auth.access_attempted","auth.access_denied", ...]` (the `auth.*` kinds) plus the new payload filters, and calls `Events().List`. Short fresh transaction per the cascade-graph read discipline (mirror `handleListEvents`).
2. Register `registerAuditRoutes` at the router-setup site `app.go:215` (where `registerEventsRoutes` is registered). Also add `registerAuditRoutes` to the synthetic router in `TestRegistryCoversRouter` (`actions_test.go:206`, builds a parallel router at lines ~217-230 to assert every mounted route is registry-known) so `/audit`'s gate-coverage is asserted.

**Verification:** `go test ./test/scenarios/... -run Audit -count=1` (a `audit:read` key can filter `/audit` by actor/action/result/mode; a key without it gets 403).

### Task 15: Migration 003 — auth-payload expression indexes (both drivers)

**Files:** `lib/foundation/persistence/postgres/migrations/003-audit-read-indexes.sql`, `lib/foundation/persistence/sqlite/migrations/003-audit-read-indexes.sql`

> No existing JSON-expression index — this is new ground. Mirror the partial-index style already in `001-schema.sql` (`... WHERE phase = 'pending'`).

**Steps:**
1. Postgres: partial expression indexes scoped to the auth kinds, e.g.
   ```sql
   CREATE INDEX rimsky_events_audit_key_id_idx ON rimsky_events ((payload->>'key_id')) WHERE kind LIKE 'auth.%';
   CREATE INDEX rimsky_events_audit_action_idx ON rimsky_events ((payload->>'action')) WHERE kind LIKE 'auth.%';
   CREATE INDEX rimsky_events_audit_status_idx ON rimsky_events ((payload->>'response_status')) WHERE kind LIKE 'auth.%';
   CREATE INDEX rimsky_events_audit_mode_idx   ON rimsky_events ((payload->>'mode'))           WHERE kind LIKE 'auth.%';
   ```
   (`occurred_at` is already indexed.)
2. SQLite: equivalents via `json_extract(payload,'$.key_id')` etc., same partial `WHERE kind LIKE 'auth.%'`.
3. Verify the migration runner picks up `003-*` (numbered, append-only).

**Verification:** `go test ./lib/foundation/persistence/... -run Migrate -count=1` (migrations apply cleanly on both drivers).

---

## Pass 5: Lineage prune dry-run count

**Goal:** Prune dry-run returns a real would-prune count.
**Scope:** Tasks 16–17
**End state:** working
**Verification:** `go test ./lib/foundation/persistence/... ./lib/control/controlapi/... -run 'Lineage|Prune' -count=1`

### Task 16: Add `CountOlderThan` to `LineageTable` (both drivers + conformance)

**Files:** `lib/foundation/persistence/lineage.go`, `.../postgres/lineage.go`, `.../sqlite/lineage.go`, `.../conformance/` (lineage conformance file)

Current interface (`lineage.go:104-107`): `DeleteOlderThan(ctx, cutoff) (int, error)`.

**Steps:**
1. Add `CountOlderThan(ctx context.Context, cutoff time.Time) (int, error)` to the interface, mirroring `DeleteOlderThan`'s shape and its "older than cutoff AND artifact no longer present" predicate — but `SELECT count(*)` instead of `DELETE`.
2. Implement in postgres + sqlite drivers (same WHERE as `DeleteOlderThan`).
3. Add a conformance test (closest cousins for shape: `testLineageQueryByParentRunID` in `conformance/lineage.go:34` for fixture setup): insert prunable + non-prunable rows; assert `CountOlderThan` equals what `DeleteOlderThan` would delete for the same cutoff. **Wire it via a `t.Run(...)` entry in `conformance/conformance.go`** — a `testXxx` function not registered there never executes (both drivers run the suite).

**Verification:** `go test ./lib/foundation/persistence/... -run Lineage -count=1`.

### Task 17: Prune dry-run returns the count

**Files:** `lib/control/controlapi/lineage.go`

Current dry-run (`lineage.go:96-100`) returns `would_have_pruned: {before}`.

**Steps:**
1. In `handleLineagePrune`, change the dry-run branch to call `deps.Persist.Lineage().CountOlderThan(req.Context(), cutoff)` and return `WriteDryRunResponse(w, req, "would_have_pruned", {"before": body.Before, "count": n})`.

**Verification:** `go test ./lib/control/controlapi/... -run Prune` (dry-run `count` == live `deleted` for the same cutoff).

---

## Pass 6: Backfill target validation

**Goal:** `backfill:create` rejects a target that is not a fan-out node wired for the override.
**Scope:** Task 18
**End state:** working
**Verification:** `go test ./lib/control/controlapi/... -run Backfill -count=1`

> **Load-bearing property:** reject (400) at submit; never accept-and-silently-degrade to a plain invalidate.

### Task 18: Validate `target_node` is a fan-out node wired for the override

**Files:** `lib/control/controlapi/backfills.go` (the create handler, ~lines 74-77 currently only checks non-empty); read `lib/runtime/backfill.go` for what "wired for the override" means (the target's `fan_out.partition_request` references trigger substitution).

**Steps:**
1. After resolving the instance + its template, look up `target_node` in the template's node set. Reject `400` if: the node doesn't exist, has no `fan_out` block, or its `fan_out.partition_request` does not reference trigger substitution (i.e. cannot consume `partition_request_override`). Message names the requirement.
2. Apply the same validation in the dry-run branch (a bad target fails identically in preview).

**Verification:** `go test ./lib/control/controlapi/... -run Backfill` — non-fan-out target → 400; valid wired fan-out target → accepted; dry-run of a bad target → 400.

---

## Pass 7: Backfill override wiring (fan-out acquisition substitution)

**Goal:** Make a backfill's `partition_request_override` actually reach the fan-out node — substitute the node's `partition_request` at acquisition from the triggering message (today it's passed verbatim, so the override is silently dropped and the `|`-fallback always fires).
**Scope:** Tasks 19–20
**End state:** working
**Verification:** `go test ./lib/foundation/persistence/... ./lib/runtime/... ./test/scenarios/... -run 'Message|Backfill|FanOut' -count=1`

> **Load-bearing property:** the fan-out `partition_request` is resolved through substitution at acquisition, binding the triggering message's override — not passed verbatim. The Task 20 test asserts a backfill's override *changes the partitions processed* (vs the template default); a happy-path "it ran" check is insufficient.

### Task 19: Add a `FrameID` filter to `MessageListFilter` (both drivers + conformance)

**Files:** `lib/foundation/persistence/messages.go`, `.../postgres/messages.go`, `.../sqlite/messages.go`, `.../conformance/`

Current (`messages.go:54-62`): filter has `InstanceID/Kind/SenderKind/Target/BackfillOperationID/DeliveredAfter/DeliveredBefore`. `MessageRow.FrameID *shared.UUID` exists (`:36`). `List` is `List(ctx, filter, pag) (PaginatedListResult[MessageRow], error)` — **no `tx` parameter**; results come back on `result.Rows`.

**Steps:**
1. Add `FrameID *shared.UUID` to `MessageListFilter`.
2. Implement the `frame_id = ?` predicate in both drivers' `List`.
3. Conformance (new ground — no existing message-list conformance test; closest cousin for list-filter shape is `testEventsListDescending` at `conformance/observability.go:141`): insert messages with/without a frame id; assert filtering by `FrameID` returns only that frame's messages. **Wire it via a `t.Run(...)` entry in `conformance/conformance.go`.**

**Verification:** `go test ./lib/foundation/persistence/... -run Message -count=1`.

### Task 20: Substitute `partition_request` at fan-out acquisition

**Files:** `lib/runtime/runner_acquire_helpers.go` (`acquireFanOutIfDeclared`, ~lines 37-122), `lib/graph/attribute/substitution.go` (reuse `SubstituteValue`)

Current (`runner_acquire_helpers.go:85-100`): passes `PartitionRequest: []byte(nodeDef.FanOut.PartitionRequest)` to `AcquireSubClaims` **verbatim** — the in-code comment says "until the substitution-aware caller lands, the literal bytes … flow through verbatim." `frameID` is in scope at this call site (`AcquireSubClaimsInput{... FrameID: &frameID ...}`).

**Steps:**
1. Before the `AcquireSubClaims` call, fetch the frame's delivered message(s) via the Task 19 lookup: `args.Persist.Messages().List(ctx, persistence.MessageListFilter{FrameID: &frameID}, persistence.ListPagination{...})`. **Confirm the row exists at this point** — message delivery marks `frame_id` and invalidates the target node *before* the resulting node-run is acquired, so the delivered message is present at fan-out acquisition (verify this ordering while wiring; it's the load-bearing assumption).
2. Build a `ResolveContext` with the params/claims already reachable here plus the frame's message payload bound to `ResolveContext.TriggerMessagePayload` (`substitution.go:94`), the slot `resolveTriggerValue` (`:438`) reads. If exactly one delivered message → set `TriggerMessagePayload` to its payload; if zero or more than one → leave it empty (the directive's `|`-fallback / `ErrMissingSource` handles it — the delivery-semantics pass, Pass 8, ensures "more than one with conflicting overrides" doesn't happen).
3. Run `nodeDef.FanOut.PartitionRequest` through the substitution engine with that context and pass the **substituted** bytes (not the literal template bytes) to `AcquireSubClaims`. Use whichever entry fits a single value — `attributes.SubstituteValue` (`substitution.go:222`, whole-value) vs `attributes.Substitute` (used elsewhere in the runtime, e.g. `runner_locks.go:122`); confirm which when wiring. Remove/replace the "literal bytes flow through verbatim" comment.

**Verification:** `go test ./lib/runtime/... ./test/scenarios/... -run 'Backfill|FanOut' -count=1` — a backfill on a `serial_queue` instance processes the **override's** partitions, not the template default (the regression test for the silent bug).

---

## Pass 8: Delivery semantics (serial_queue default + conflict-aware coalesce)

**Goal:** Make `serial_queue` the default `FrameDeliveryMode`, and make `coalesce` deliver in received-order until a substitution value-conflict (so distinct overrides don't collide into one rerun).
**Scope:** Tasks 21–22
**End state:** working
**Verification:** `go test ./lib/runtime/... ./test/scenarios/messages/... -run 'Delivery|Coalesce|Serial' -count=1`

> **Load-bearing property:** under `coalesce`, two messages that would resolve a node's substitution to *different* values must land in *separate* frames (no silent override loss); same-value messages may coalesce. The Task 22 test drives two distinct-override backfills under coalesce and asserts both are processed, in order.
> **Scope guard:** this touches **`FrameDeliveryMode`** (per-instance message delivery) only — **not** `FrameResolutionMode` (the template-driven frame-aggregation knob on `rimsky_frames`), a separate concept that stays as-is.

### Task 21: Flip the default `FrameDeliveryMode` to `serial_queue`

**Files:** `lib/foundation/persistence/postgres/instances.go`, `lib/foundation/persistence/sqlite/instances.go`, `lib/runtime/message_delivery.go`, new `lib/foundation/persistence/postgres/migrations/004-frame-delivery-default.sql` + `.../sqlite/migrations/004-frame-delivery-default.sql`

Current: `rimsky_instances.frame_delivery_mode TEXT NOT NULL DEFAULT 'coalesce'` (postgres `001-schema.sql:61`, sqlite `:62`); code fallback `mode := FrameDeliveryCoalesce` in `deliverForRunningFrame` (`message_delivery.go:193`).

> **The default is NOT taken from the column DEFAULT.** Both drivers hardcode the literal in the INSERT — `VALUES (… COALESCE($6, 'coalesce') …)` (`postgres/instances.go:78`) and `COALESCE(?, 'coalesce')` (`sqlite/instances.go:76`) — so an omitted mode writes `'coalesce'` to the row regardless of the column DEFAULT. The "Empty string → fall through to the column DEFAULT" comments (`postgres/instances.go:61`, `sqlite/instances.go:51`) are misleading. Flipping the migration default alone does **nothing** for new instances; you must flip the insert literals.

**Steps:**
1. Code fallback: change `deliverForRunningFrame`'s default from `FrameDeliveryCoalesce` to `FrameDeliverySerialQueue` (`message_delivery.go:193-194`).
2. **Insert literal (the load-bearing change):** change `COALESCE($6, 'coalesce')` → `COALESCE($6, 'serial_queue')` in `postgres/instances.go:78` and `COALESCE(?, 'coalesce')` → `COALESCE(?, 'serial_queue')` in `sqlite/instances.go:76`. Correct the misleading comments (`postgres/instances.go:61`, `sqlite/instances.go:51`) to say the insert defaults an omitted mode to `serial_queue` (per "Fix Every Bug You Find," the inaccurate comment gets fixed regardless).
3. Column default (belt-and-suspenders for any other insert path / `migrate_test`): migrations are numbered + append-only, so **add** `004-frame-delivery-default.sql` in each driver dir that `ALTER`s `rimsky_instances.frame_delivery_mode`'s default to `'serial_queue'` (do not edit `001-schema.sql` in place; `003-*` is the audit-read indexes from Pass 4).

**Verification:** `go test ./lib/foundation/persistence/... ./test/scenarios/messages/... -run 'Migrate|Delivery' -count=1` (migrations apply on both drivers; a freshly-created no-mode instance delivers one message per frame).

### Task 22: Make `coalesce` delivery conflict-aware

**Files:** `lib/runtime/message_delivery.go` (`DeliverPendingMessages` ~395-438, `deliverForRunningFrame` ~181-235, `cascadeMessageSubscribersInTx` ~275-393)

Current: coalesce sets `deliverSet = live` (all pending into one frame); serial_queue sets `deliverSet = live[:1]`. `cascadeMessageSubscribersInTx` (run *after* delivery) loads the template and calls `ExtractSubstitutionRefsFromTemplate` / `BuildSubscriptionEdges` to match subscribers.

**Steps:**
1. Change the coalesce branch of the deliver-set decision: instead of `deliverSet = live`, walk `live` in received-order and accumulate into the frame **until** a message would resolve a payload-reading node's trigger substitution to a value *different* from one an already-accumulated message bound for the same node — then stop (remaining messages stay pending for the next frame). Same-value (idempotent) bindings keep coalescing.
2. Conflict detection needs, per candidate message, the payload-reading nodes it would invalidate — reuse the template + `ExtractSubstitutionRefsFromTemplate` / `BuildSubscriptionEdges` matching that `cascadeMessageSubscribersInTx` already performs, computed *as part of* the deliver-set decision (today it runs after). Restructure `deliverForRunningFrame` so the match info is available when choosing `deliverSet`. A conservative comparison (different message payloads targeting the same payload-reading node → conflict) is acceptable.
3. serial_queue is unchanged (`deliverSet = live[:1]`).

**Verification:** `go test ./lib/runtime/... ./test/scenarios/messages/... -run 'Coalesce' -count=1` — under coalesce: two same-override messages coalesce into one frame; two different-override backfills split into consecutive frames, both processed in order.

---

## Pass 9: Design-doc mutations

**Goal:** Apply the spec's `## Design changes` to the concept catalog + create the tension + regenerate the TOC.
**Scope:** Tasks 23–25
**End state:** working
**Verification:** `go build ./...` (docs don't affect build) + the structural greps in each task.

> Concept-body text must follow the self-containment rule (no file paths, no `code:`/`pkg:` citations, no external-doc refs in concept body; spec slugs allowed only in dated Notes). Each edit appends a Notes entry dated 2026-05-29 citing `spec:2026-05-29-console-upstream-auth-audit-and-fixes`.

### Task 23: Mutate the concepts

**Files:** `.ok-planner/design/concepts/{dry-run,permission,event-log,backfill,role-template,fan-out,message,frame}.md` (`message.md` owns `frame_delivery_mode`; `frame.md` only cross-references it).

**Steps (apply each per the spec's `## Design changes` bullets, verbatim where the spec gives new text):**
1. `dry-run.md` — rewrite "What it is" (per-request flag, not grant modifier), "Purpose" (human-in-the-loop preview + validate-without-commit; **delete the graduated-trust/agent-promotion narrative**), "Boundaries" (covers all writes; no auth carve-out; no per-grant mode vocabulary), "Invariants" (reads = no-op `executed:true`; every write previewable, structurally guaranteed; forced dry-run never mutates; **remove** "Auth mutations are NOT dry-runnable").
2. `permission.md` — grant entry is an action string, no `mode`; evaluator = set-membership; **remove** "first-match-wins (for mode)", "Read actions ignore mode", "Auth mutations are NOT dry-runnable"; add `audit:read` to the canonical action list.
3. `event-log.md` — add a durability invariant (writes durable, never silently dropped; per-request auth audit write is synchronous); add `audit:read` as a reader of the `auth.*` rows.
4. `backfill.md` — change the target-validation invariant from "warning if not …" to "`backfill:create` **rejects (400)** a target that is not a fan-out node wired to accept the partition override." **And** correct the override description: the `partition_request_override` now actually reaches the fan-out node (substituted at acquisition); before, it was silently ignored and the `|`-fallback always fired.
5. `role-template.md` — remove the `--dry-run=<action>` patch-operator from Boundaries (line ~29) and **delete** the line-~34 invariant ("`--dry-run=<action>` rejects … the handlers ignore dry-run mode anyway"); the `--dry-run` operator no longer exists and auth mutations are now dry-runnable.
6. `fan-out.md` — state that a fan-out node's `partition_request` is resolved through substitution at acquisition (`{{trigger.message.payload.partition_request_override | <template-default>}}` binds the triggering message's override), not passed verbatim. **Also fix the pre-existing doc bug**: the current text's `| default: <x>` is not the engine's grammar (the fallback is `<directive> | <literal>`, no `default:` keyword) — drop the `default:` keyword from the illustrative directive.
7. `message.md` (owner of `frame_delivery_mode`) — record: (1) the default `FrameDeliveryMode` is **`serial_queue`** (one message per frame; intuitive default; coalesce opt-in); (2) `coalesce` delivers in received-order until a message would resolve a node's substitution to a conflicting (different) value, then breaks the frame (same-value bindings coalesce freely). Update **all three** spots that currently assert coalesce-default / coalesce-delivers-all (Definition, Boundaries "Owns: … delivery semantics," Invariants), not just one. Explicitly distinguish `FrameDeliveryMode` (per-instance message delivery) from `FrameResolutionMode` (template-driven frame aggregation, unchanged).
8. `frame.md` — correct any parenthetical that names `coalesce` as the delivery default to `serial_queue` (it only cross-references `concept:message`; its own owned knob `FrameResolutionMode` is unchanged). Notes entry only if edited.

**Verification:** `rg -n '2026-05-29' .ok-planner/design/concepts/{dry-run,permission,event-log,backfill,role-template,fan-out,message}.md` shows a Notes entry in each; `rg -n 'graduated|promote|first-match-wins|NOT dry-runnable|ignore dry-run mode' .ok-planner/design/concepts/` returns nothing in those files; `concepts/message.md` mentions `serial_queue` as the delivery default and no longer asserts coalesce-default.

### Task 24: Doc-accuracy fix to named-event + node-subscription; create the tension

**Files:** `.ok-planner/design/concepts/{named-event,node-subscription}.md`; new `.ok-planner/design/tensions/event-vocabulary-implies-delivery.md`

**Steps:**
1. `named-event.md` — state plainly: consumed invalidate-then-pull; subscribing fires the receiver **once per frame regardless of emission count**; receiver pulls the **latest** emission; named events **never create a frame** and do **not** fan out per-emission. **Named events are not a fan-out mechanism**: true per-item (parallel) fan-out is `concept:fan-out` (claim-producer split-scope); sequential per-message processing is `serial_queue` message delivery (see `concept:message`). Soften "carries a payload" delivery phrasing. Notes entry 2026-05-29.
2. `node-subscription.md` — correct the 2026-05-20 "subscriptions remain push: an upstream transition causes the receiver to fire" line: the receiver is **invalidated and rescheduled, then pulls** the latest persisted values; nothing rides the cascade edge. State the event-subscription cardinality (one dispatch per frame, latest-only). Notes entry 2026-05-29.
3. Create `tensions/event-vocabulary-implies-delivery.md` (status: open; `affects: [named-event, node-subscription]`). `## What is muddy`: the pub-sub vocabulary models delivery but the engine is invalidate-then-pull, and the mismatch has misled a design. `## Resolution candidates` (**path-free** — no sketch/spec/file references): rename the reactive vocabulary toward invalidation terms (event→response, subscribe→watch, payload→body, and drop the redundant `trigger.` wrapper), decided in a future `/refine-design`. (Do NOT copy any sketch slug into the tension body.)

**Verification:** `rg -n '2026-05-29' .ok-planner/design/concepts/{named-event,node-subscription}.md` shows Notes entries; `test -f .ok-planner/design/tensions/event-vocabulary-implies-delivery.md`; `rg -n '\.md|code:|/' .ok-planner/design/tensions/event-vocabulary-implies-delivery.md` shows no path-form citation in Resolution candidates.

### Task 25: Regenerate `concepts.md` TOC

**Files:** `.ok-planner/design/concepts.md`

**Steps:**
1. Regenerate the TOC per the documented format (sorted alphabetical bullet list of `<slug> — <one-sentence definition>`, optional `(aliases: ...)`), reflecting the updated one-line definitions for `dry-run` and `permission` (which changed shape). Other entries' one-line defs are unchanged (backfill/fan-out/message/frame changed invariants/mechanism, not their one-sentence definition). (No new concept files were added; the new artifact is a *tension*, which does not appear in `concepts.md`.)

**Verification:** `rg -n 'dry-run|permission' .ok-planner/design/concepts.md` shows the updated one-line definitions; the list stays alphabetical.

---

## Manual checks after completion

(None — every verification above is a runnable command. The dashboard-side consumption of `/audit`, the dry-run flag, and the trigger-message body is a separate console-side concern and not part of this plan.)
