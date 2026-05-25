# Instance Debugger — Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-24-instance-debugger-design.md`
**Goal:** Land the agent-debugger surface on `concept:control-api`: runtime-installed breakpoints (matcher + signal_type, pause/notify modes, resume-with-overlay), soft-pause/resume on `concept:instance`, and MCP `resources/read` polling for breakpoint hits. Plus prerequisite work: flatten the persistence migrations into a single `001-schema.sql` per backend, and extract the matcher grammar to `foundation/matcher/`.
**Architecture:** Two new persistence tables (`rimsky_instance_breakpoints`, `rimsky_breakpoint_hits`) and a new column (`rimsky_instances.paused`) land in a freshly-consolidated baseline schema. Supervisor checkpoints in `runtime/runner_dispatch.go` (before_dispatch) and `runtime/runner_terminal.go` (after_terminal) query matching breakpoints, write hit rows, and block (pause mode) or continue (notify_only). MCP server extends from tools-only to tools + read-only resources; agent polls `resources/read` with a `?since=<seq>` cursor. No push, no subscription, no `pg_notify` — polling-only across the stack. The matcher grammar from `attribute_overrides.by_match` extracts to a shared `foundation/matcher/` package consumed by both the by_match site and the new breakpoint code.
**Tech Stack:** Go (foundation + root modules tied via `go.work`), `jackc/pgx/v5` for Postgres, `modernc.org/sqlite` for SQLite (pure-Go, no CGO), `go-chi/chi` for HTTP routing, stdlib `log/slog` for logging. JSON Schema validation via the existing `graph/attribute` package. Testcontainers-go for scenario + persistence tests (Postgres in Docker).

---

## Dependencies and ordering constraints

**External dependency:** `plan:2026-05-23-signal-taxonomy-and-policy-decoupling` Pass 1 (signal infrastructure + audit-emission wiring) must be landed. The breakpoint feature uses `code:foundation/signal/taxonomy.go::ValidateTypePath` and `code:foundation/signal/types.go::TypePath.HasPrefix` in the `signal_type` filter for `after_terminal` breakpoints. The first task gates execution on the presence of those symbols.

**Internal ordering:**

- Pass 1 (schema reset) is precondition to everything else (persistence tests need a valid migration).
- Pass 2 (`foundation/matcher/` extraction) is precondition to Pass 4 and Pass 5 (control-api validator and supervisor evaluator both call the extracted package).
- Pass 3 (persistence layer) is precondition to Pass 4 (control-api handlers call persistence) and Pass 5 (supervisor calls persistence).
- Passes 4–6 can land in any order; we use the natural sequence (control-api routes → supervisor cooperation → MCP wire shape).
- Pass 7 (reaper integration) requires Pass 3.
- Pass 8 (scenario tests) requires Passes 1–7 to be functionally complete.
- Pass 9 (concept-doc mutations) is independent of code but listed last because the docs reflect the landed shape.

**End state of each pass except Pass 1:** `working`. Pass 1 itself ends `working` — the migration flattening preserves all current schema content; no temporary broken state is necessary.

---

## Pass 1: Schema reset, signal-package gate, persistence row types

**Goal:** Flatten the 14 existing migrations into a single consolidated `001-schema.sql` per backend, add the new breakpoint tables and `rimsky_instances.paused` column, and declare the persistence row-type Go structs for breakpoints. Gate on the signal-taxonomy plan's Pass 1 being landed.

**Scope:** Tasks 1–6
**End state:** working
**Verification:** `go build ./... && go test ./foundation/persistence/... -count=1 && make lint`

### Task 1: Verify the signal-package dependency

**Files:** Read-only check.

**Steps:**
1. Run `grep -n "func ValidateTypePath" foundation/signal/taxonomy.go`. Confirm a match exists.
2. Run `grep -n "func (t TypePath) HasPrefix" foundation/signal/types.go`. Confirm a match exists.
3. Run `grep -n "type Signal struct" foundation/signal/types.go`. Confirm a match exists.
4. If any of these are missing, **stop the plan** and report to the user that the signal-taxonomy plan's Pass 1 has not landed. Do not proceed with the rest of the plan — its code paths depend on these symbols.

**Verification:** All three greps return at least one line. If not, plan execution halts here.

### Task 2: Read the existing 14 migration files (information capture)

**Files:** Read-only:
- `foundation/persistence/postgres/migrations/001-baseline.sql` through `014-drop-last-outcome.sql` (14 files)
- `foundation/persistence/sqlite/migrations/001-baseline.sql` through `014-drop-last-outcome.sql` (14 files)
- `foundation/persistence/postgres/migrations/embed.go` and `foundation/persistence/sqlite/migrations/embed.go`

**Steps:**
1. Read each of the 28 .sql files and capture the cumulative schema state they produce. Write the new `001-schema.sql` files (Tasks 3 and 4) before deleting the originals.
2. Confirm both `embed.go` files use `//go:embed *.sql` (they do — the embed pattern globs the directory and needs no edit when files are added or removed). Note this fact; no edit required at any point in the plan.

**Verification:** None — read-only information capture. The migration files remain on disk until Task 5.

### Task 3: Write consolidated `001-schema.sql` for Postgres

**Files:** `foundation/persistence/postgres/migrations/001-schema.sql` (new)

**Steps:**
1. Create the new file containing the full current schema state (post-migration-014, so `last_outcome` is NOT declared on `rimsky_node_runs`) PLUS the new breakpoint tables and the new `paused` column on `rimsky_instances`. The schema must include all of the following CREATE TABLE statements, with current-state columns, indexes, and CHECK constraints:
   - `rimsky_migrations` (idempotency tracker for the migration runner itself)
   - `rimsky_templates`, `rimsky_template_tags`
   - `rimsky_instances` — **add new column `paused BOOLEAN NOT NULL DEFAULT false`**; keep existing columns including `attribute_overrides JSONB` (renamed from `userdata_overrides` per migration 005), `attribute_overrides_match_counts JSONB` (added by migration 006), `main_run_scope_id UUID` (added by migration 010), `frame_delivery_mode TEXT`, and the existing UNIQUE constraint on `(template_hash, instance_key)`.
   - `rimsky_supervisors`
   - `rimsky_run_scopes` (per migration 007, with the columns and tree shape established there)
   - `rimsky_nodes`, `rimsky_node_runs` — **omit `last_outcome`** (dropped by migration 014); include `run_scope_id UUID` FK (migration 008), `settling_signal_type TEXT` (migration 013), and the post-collapse `parked_reason` CHECK constraint (migration 011).
   - `rimsky_node_attributes` per migration 003 shape
   - `rimsky_node_events`
   - `rimsky_claim_handles`, `rimsky_claim_holders` (with the `claim_scope BYTEA` column name per migration 009 — formerly `scope`)
   - `rimsky_named_locks`
   - `rimsky_wait_set` (with `drained_at TIMESTAMPTZ` per migration 004)
   - `rimsky_frames`
   - `rimsky_messages`, `rimsky_message_idempotencies`, `rimsky_publisher_subscriptions`
   - `rimsky_lifecycle_idempotencies`
   - `rimsky_events`
   - `rimsky_lineage`
   - `rimsky_api_keys`
   - `rimsky_blob_orphans`
   - **NEW: `rimsky_instance_breakpoints`** (per spec §7.2):
     ```sql
     CREATE TABLE rimsky_instance_breakpoints (
       id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
       instance_id      UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
       matcher          JSONB NOT NULL,
       checkpoint       TEXT NOT NULL
                        CHECK (checkpoint IN ('before_dispatch','after_terminal')),
       signal_type      TEXT,
       mode             TEXT NOT NULL DEFAULT 'pause'
                        CHECK (mode IN ('pause','notify_only')),
       overflow_policy  TEXT NOT NULL
                        CHECK (overflow_policy IN ('drop_oldest','block_dispatch','auto_resume_after_ttl')),
       hit_ttl_seconds  INT NOT NULL DEFAULT 300,
       ttl_seconds      INT,
       dropped_count    BIGINT NOT NULL DEFAULT 0,
       created_by_key   TEXT NOT NULL,
       created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       expires_at       TIMESTAMPTZ
     );

     CREATE INDEX idx_breakpoints_instance_active
       ON rimsky_instance_breakpoints (instance_id)
       WHERE expires_at IS NULL OR expires_at > NOW();

     CREATE INDEX idx_breakpoints_expires
       ON rimsky_instance_breakpoints (expires_at)
       WHERE expires_at IS NOT NULL;
     ```
   - **NEW: `rimsky_breakpoint_hits`** (per spec §7.2):
     ```sql
     CREATE TABLE rimsky_breakpoint_hits (
       seq             BIGSERIAL PRIMARY KEY,
       id              UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
       breakpoint_id   UUID NOT NULL REFERENCES rimsky_instance_breakpoints(id) ON DELETE CASCADE,
       instance_id     UUID NOT NULL REFERENCES rimsky_instances(id),
       node_run_id     UUID,
       frame_id        UUID,
       checkpoint      TEXT NOT NULL,
       mode            TEXT NOT NULL,
       snapshot        JSONB NOT NULL,
       hit_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       resumed_at      TIMESTAMPTZ,
       resumed_by_key  TEXT,
       resume_overlay  JSONB
     );

     CREATE INDEX idx_bp_hits_breakpoint_unresumed
       ON rimsky_breakpoint_hits (breakpoint_id, hit_at)
       WHERE resumed_at IS NULL;

     CREATE INDEX idx_bp_hits_instance_seq
       ON rimsky_breakpoint_hits (instance_id, seq);

     CREATE INDEX idx_bp_hits_breakpoint_seq
       ON rimsky_breakpoint_hits (breakpoint_id, seq);
     ```
2. Source-of-truth references for current-state columns: the existing migration files (still on disk per Task 2's read-only nature). The consolidated file is a faithful merge of `001-baseline.sql` plus `002` through `014`'s cumulative modifications, with the new columns/tables added.
3. **Ordering inside the file:** declare tables in dependency order so that foreign key references resolve. Roughly: `rimsky_migrations` first, then `rimsky_templates`, `rimsky_template_tags`, `rimsky_supervisors`, `rimsky_instances`, `rimsky_run_scopes`, `rimsky_nodes`, `rimsky_node_runs`, `rimsky_node_attributes`, ... and the breakpoint tables last (they reference `rimsky_instances`).
4. Add a header comment at the top of `001-schema.sql`:
   ```sql
   -- Rimsky consolidated schema baseline.
   -- Created 2026-05-24 by spec .ok-planner/specs/2026-05-24-instance-debugger-design.md.
   -- Replaces the prior 14-migration sequence (001-baseline through 014-drop-last-outcome).
   -- Pre-v1 break-freely operation per .claude/rules/rules.md — operators with existing
   -- dev databases drop and recreate; this is NOT an upgrade path.
   ```

**Verification:** `go test ./foundation/persistence/postgres/... -count=1` (the postgres-package tests bring up a testcontainers Postgres, run the migration, and exercise the tables; this verifies the consolidated schema applies cleanly).

### Task 4: Write consolidated `001-schema.sql` for SQLite

**Files:** `foundation/persistence/sqlite/migrations/001-schema.sql` (new)

**Steps:**
1. Mirror Task 3 for SQLite, with SQLite-specific syntax adjustments. The prior baseline at `foundation/persistence/sqlite/migrations/001-baseline.sql` (read before deletion in Task 4 step 4 below) is the source of truth for SQLite's syntax conventions. Concretely:
   - `UUID` columns: `TEXT` (SQLite has no native UUID type).
   - **`JSONB` columns: `TEXT`** with the caller marshaling/unmarshaling JSON. This is the codebase convention — the prior baseline's header explicitly maps `JSONB → TEXT (caller marshals JSON)`. Do NOT declare `JSONB` literally; modernc.org/sqlite tolerates the type name but the existing convention is `TEXT`.
   - `BIGSERIAL`: `INTEGER PRIMARY KEY AUTOINCREMENT` (only one column per table can be PRIMARY KEY this way; for `rimsky_breakpoint_hits`, the `seq` column gets `INTEGER PRIMARY KEY AUTOINCREMENT`; the `id` UUID column is declared as `TEXT NOT NULL UNIQUE` with the existing SQLite UUID default expression copied verbatim).
   - `TIMESTAMPTZ`: `TIMESTAMP` or `DATETIME` per existing SQLite migration conventions.
   - `gen_random_uuid()`: replace with the existing SQLite default-UUID expression already used in the codebase (find an example in the prior `001-baseline.sql` and reuse verbatim).
   - Partial indexes (`WHERE` clauses on `CREATE INDEX`) are supported in modernc.org/sqlite — keep them, matching the existing baseline's index style.
2. Include all the same tables and constraints as the Postgres baseline, adjusted for SQLite syntax.
3. Apply the same header comment as Task 3.
4. **Delete the 14 old migration files** in both backends:
   - In `foundation/persistence/postgres/migrations/`, delete `001-baseline.sql` through `014-drop-last-outcome.sql` (14 files). Keep the new `001-schema.sql` and `embed.go`.
   - In `foundation/persistence/sqlite/migrations/`, delete the same 14 files. Keep the new `001-schema.sql` and `embed.go`.
   - `embed.go` is unchanged in both directories — its `//go:embed *.sql` pattern globs whatever .sql files remain.
5. Run `ls foundation/persistence/postgres/migrations/*.sql` and `ls foundation/persistence/sqlite/migrations/*.sql`. Confirm each returns only `001-schema.sql`.

**Verification:** `go test ./foundation/persistence/sqlite/... ./foundation/persistence/postgres/... -count=1` (the per-backend tests run the migration against in-memory SQLite and a testcontainers Postgres, exercising the consolidated schema cleanly).

### Task 5: Declare persistence row types for breakpoints

**Files:** `foundation/persistence/breakpoints.go` (new)

**Steps:**
1. Create `foundation/persistence/breakpoints.go` with the following content. Follow the existing convention of one file per row-type accessor (see `foundation/persistence/instances.go`, `foundation/persistence/nodes.go` for shape):

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   package persistence

   import (
       "context"
       "time"

       "github.com/fallguyconsulting/rimsky/foundation/shared"
   )

   // Typed-string enums for the breakpoint vocabulary. The SQL schema
   // CHECK constraints carry the same string values (the schema can't
   // reference Go constants); these typed constants are the canonical
   // Go-side surface. Validators, runtime evaluators, and HTTP handlers
   // reference these instead of bare string literals.
   //
   // @concept: breakpoint
   type BreakpointCheckpoint string
   const (
       CheckpointBeforeDispatch BreakpointCheckpoint = "before_dispatch"
       CheckpointAfterTerminal  BreakpointCheckpoint = "after_terminal"
   )

   type BreakpointMode string
   const (
       BreakpointModePause      BreakpointMode = "pause"
       BreakpointModeNotifyOnly BreakpointMode = "notify_only"
   )

   type BreakpointOverflowPolicy string
   const (
       OverflowDropOldest        BreakpointOverflowPolicy = "drop_oldest"
       OverflowBlockDispatch     BreakpointOverflowPolicy = "block_dispatch"
       OverflowAutoResumeAfterTTL BreakpointOverflowPolicy = "auto_resume_after_ttl"
   )

   // BreakpointRow is the Go projection of rimsky_instance_breakpoints.
   // Per concept:breakpoint (introduced by spec
   // .ok-planner/specs/2026-05-24-instance-debugger-design.md).
   //
   // @concept: breakpoint
   type BreakpointRow struct {
       ID              shared.UUID
       InstanceID      shared.UUID
       Matcher         map[string]any
       Checkpoint      BreakpointCheckpoint
       SignalType      *string // nullable; only set for after_terminal
       Mode            BreakpointMode
       OverflowPolicy  BreakpointOverflowPolicy
       HitTTLSeconds   int
       TTLSeconds      *int    // nullable; instance-lifetime if null
       DroppedCount    int64
       CreatedByKey    string
       CreatedAt       time.Time
       ExpiresAt       *time.Time // nullable; materialized from TTLSeconds at create
   }

   // BreakpointTable is the per-row-type accessor on rimsky_instance_breakpoints.
   type BreakpointTable interface {
       Create(ctx context.Context, bp BreakpointRow, tx Tx) (shared.UUID, error)
       Get(ctx context.Context, id shared.UUID, tx Tx) (*BreakpointRow, error)
       ListForInstance(ctx context.Context, instanceID shared.UUID, includeExpired bool, tx Tx) ([]BreakpointRow, error)
       Delete(ctx context.Context, id shared.UUID, tx Tx) error
       IncrementDropped(ctx context.Context, id shared.UUID, tx Tx) error
       SweepExpired(ctx context.Context, now time.Time, tx Tx) (int, error)
   }

   // BreakpointHitRow is the Go projection of rimsky_breakpoint_hits.
   //
   // @concept: breakpoint
   type BreakpointHitRow struct {
       Seq            int64        // monotonic cursor for resources/read pagination
       ID             shared.UUID  // stable identity for the resume API
       BreakpointID   shared.UUID
       InstanceID     shared.UUID
       NodeRunID      *shared.UUID
       FrameID        *shared.UUID
       Checkpoint     BreakpointCheckpoint
       Mode           BreakpointMode
       Snapshot       map[string]any // full payload per spec §4.6
       HitAt          time.Time
       ResumedAt      *time.Time
       ResumedByKey   *string
       ResumeOverlay  map[string]any // nullable
   }

   // BreakpointHitTable is the per-row-type accessor on rimsky_breakpoint_hits.
   type BreakpointHitTable interface {
       Create(ctx context.Context, hit BreakpointHitRow, tx Tx) (id shared.UUID, seq int64, err error)
       Get(ctx context.Context, id shared.UUID, tx Tx) (*BreakpointHitRow, error)
       ListSinceForInstance(ctx context.Context, instanceID shared.UUID, sinceSeq int64, limit int, tx Tx) ([]BreakpointHitRow, error)
       ListSinceForBreakpoint(ctx context.Context, bpID shared.UUID, sinceSeq int64, limit int, tx Tx) ([]BreakpointHitRow, error)
       ListUnresumedForBreakpoint(ctx context.Context, bpID shared.UUID, tx Tx) ([]BreakpointHitRow, error)
       Resume(ctx context.Context, id shared.UUID, byKey string, overlay map[string]any, tx Tx) error
       AutoResumeStale(ctx context.Context, now time.Time, tx Tx) (int, error)
       DropOldest(ctx context.Context, bpID shared.UUID, keepCount int, tx Tx) (int, error)
       UnresumedCount(ctx context.Context, bpID shared.UUID, tx Tx) (int, error)
   }
   ```

2. Add the two accessor methods to the `Tables` interface in `foundation/persistence/tables.go`. Insert them in the section commented as introduced by recent specs:

   ```go
   // Breakpoints accessors introduced by spec
   // .ok-planner/specs/2026-05-24-instance-debugger-design.md (concept:breakpoint).
   Breakpoints() BreakpointTable
   BreakpointHits() BreakpointHitTable
   ```

3. Run `go build ./foundation/persistence/...`. Expect compilation errors in the Postgres and SQLite backend packages because they don't yet implement the new interfaces. **This is expected** — Task 6 (and Pass 3) introduce the implementations. For Pass 1's verification, the persistence package itself (`foundation/persistence/`) must build cleanly, but its impl packages will fail.

**Verification:** `go build ./foundation/persistence/` (the interface package alone) exits 0.

### Task 6: Stub the Postgres and SQLite impls to satisfy the interface (aspect-type pattern, feature-file convention)

**Files:**
- `foundation/persistence/postgres/breakpoints.go` (new)
- `foundation/persistence/postgres/breakpoint_hits.go` (new)
- `foundation/persistence/sqlite/breakpoints.go` (new)
- `foundation/persistence/sqlite/breakpoint_hits.go` (new)

**Steps:**
1. Read recent feature files for the convention: `code:foundation/persistence/postgres/api_keys.go`, `code:foundation/persistence/postgres/run_scopes.go`, `code:foundation/persistence/postgres/messages.go`. Each declares the aspect type, the compile-time assertion, the accessor method on `*tablesImpl`, AND the `q` helper **in the feature file itself**, not in `backend.go`. Follow this convention (the older aspect types in `backend.go`'s `type ( ... )` block are legacy; new features land in their own files).

2. In `foundation/persistence/postgres/breakpoints.go`:
   ```go
   // Copyright © 2026 Fall Guy Consulting. ...

   package postgres

   import (
       "context"
       "errors"
       "time"

       "github.com/fallguyconsulting/rimsky/foundation/persistence"
       "github.com/fallguyconsulting/rimsky/foundation/shared"
   )

   // breakpointsImpl is the per-row-type aspect of *tablesImpl, exposing
   // the BreakpointTable method set. Follows the same aspect-type pattern
   // as foundation/persistence/postgres/run_scopes.go and api_keys.go.
   type breakpointsImpl tablesImpl

   var _ persistence.BreakpointTable = (*breakpointsImpl)(nil)

   func (s *tablesImpl) Breakpoints() persistence.BreakpointTable { return (*breakpointsImpl)(s) }

   func (b *breakpointsImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

   // Stubbed methods — full impls in Pass 3 Task 12.
   func (b *breakpointsImpl) Create(ctx context.Context, bp persistence.BreakpointRow, tx persistence.Tx) (shared.UUID, error) {
       return shared.UUID{}, errors.New("breakpointsImpl.Create: not yet implemented (Pass 3)")
   }
   func (b *breakpointsImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.BreakpointRow, error) {
       return nil, errors.New("breakpointsImpl.Get: not yet implemented (Pass 3)")
   }
   // ... stub the rest of the BreakpointTable interface methods identically.
   ```

3. Mirror `foundation/persistence/postgres/breakpoint_hits.go`: aspect type `breakpointHitsImpl`, compile-time assertion, `BreakpointHits()` accessor on `*tablesImpl`, q helper, stubbed `BreakpointHitTable` methods.

4. Mirror the SQLite feature files at `foundation/persistence/sqlite/breakpoints.go` and `foundation/persistence/sqlite/breakpoint_hits.go`. The SQLite-side aspect-type-on-tablesImpl convention matches the Postgres-side. Cross-reference the existing `foundation/persistence/sqlite/api_keys.go` or `messages.go` for the precise pattern (the type names and helper signatures will be subtly different — SQLite has its own `tablesImpl` and `querier` types).

5. Run `go build ./...` from the repo root.

**Verification:** `go build ./... && make lint`.

---

## Pass 2: `foundation/matcher/` extraction

**Goal:** Extract the matcher grammar, evaluator, and validator from `runtime/attribute_overrides.go` and `control/controlapi/attribute_overrides.go` into a shared `foundation/matcher/` package. Both the existing by_match call sites and the upcoming breakpoint code (Passes 4 and 5) consume this package.

**Scope:** Tasks 7–11
**End state:** working
**Verification:** `go build ./... && go test ./foundation/matcher/... ./runtime/... ./control/controlapi/... -count=1 && make lint`

### Task 7: Create `foundation/matcher/matcher.go`

**Files:** `foundation/matcher/matcher.go` (new)

**Steps:**
1. Read `runtime/attribute_overrides.go` to understand the existing `evaluateMatcher`, `matcherAllowedKeys`, `walkAttrPath`, and `primitiveEqual` definitions in full.

2. Create `foundation/matcher/matcher.go` with the following content:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   // Package matcher implements the closed five-key dispatch-identity
   // predicate shared by concept:attribute (the by_match overlay) and
   // concept:breakpoint (the runtime pause-point matcher).
   //
   // Grammar: equality-only across a fixed key set
   // {node_type, executor, graph, child_key, attrs}; AND across present
   // keys; missing keys are wildcards; empty matcher fires for every
   // dispatch.
   //
   // The attrs.<path> branch is the inertness-sanctioned attribute-value
   // read site (preserved from runtime/attribute_overrides.go's prior
   // home); see concept:inertness.
   //
   // @concept: inertness (sanctioned attribute-value read site lives in Evaluate's attrs branch)
   package matcher

   import (
       "encoding/json"
       "errors"
       "strings"

       "github.com/fallguyconsulting/rimsky/foundation/shared"
   )

   // ErrInvalid is the package-local sentinel returned by Validate for any
   // grammar or cross-check failure. Callers use errors.Is(err, matcher.ErrInvalid)
   // to convert to HTTP 400 / MCP InvalidParams at their boundary. Pattern
   // mirrors the existing control/controlapi/attribute_overrides.go::errAttributeOverridesInvalid.
   var ErrInvalid = errors.New("matcher invalid")

   // Matcher is the closed-key-set predicate. The wire form is JSON;
   // the Go form is a generic map (the runtime never inspects shape
   // beyond the keys this package owns).
   type Matcher map[string]any

   // Context is the dispatch context the matcher evaluates against.
   type Context struct {
       Executor     string
       NodeType     string
       Graph        string
       ChildKey     string
       AttributeBag map[string]any // post-L5 merged attributes per concept:attribute
   }

   // allowedKeys is the closed set of recognised matcher keys.
   // The Validate function rejects unknown keys at registration; Evaluate
   // defensively skips entries with unknown keys (matching the existing
   // runtime discipline against out-of-band persistence corruption).
   var allowedKeys = map[string]struct{}{
       "node_type": {},
       "executor":  {},
       "graph":     {},
       "child_key": {},
       "attrs":     {},
   }

   // Evaluate returns true if matcher fires on ctx. AND-joined across
   // present keys; missing keys are wildcards; empty matcher matches
   // every dispatch. If the matcher carries any key outside the closed
   // allowed set, returns false and emits a Warn (out-of-band persistence
   // corruption — the validator rejects this at registration).
   //
   // The attrs.<path> branch is the concept:inertness sanctioned
   // attribute-value read site.
   func Evaluate(m Matcher, ctx Context, logger shared.Logger, entryIndex int) bool {
       // Defensive guard against unknown keys (out-of-band corruption).
       for k := range m {
           if _, ok := allowedKeys[k]; !ok {
               if logger != nil {
                   logger.Warn("matcher.Evaluate: matcher contains unknown key; skipping entry",
                       "entry_index", entryIndex,
                       "unknown_key", k)
               }
               return false
           }
       }
       if len(m) == 0 {
           return true
       }
       if v, ok := m["node_type"]; ok {
           s, _ := v.(string)
           if s != ctx.NodeType {
               return false
           }
       }
       if v, ok := m["executor"]; ok {
           s, _ := v.(string)
           if s != ctx.Executor {
               return false
           }
       }
       if v, ok := m["graph"]; ok {
           s, _ := v.(string)
           if s != ctx.Graph {
               return false
           }
       }
       if v, ok := m["child_key"]; ok {
           s, _ := v.(string)
           if s != ctx.ChildKey {
               return false
           }
       }
       if v, ok := m["attrs"]; ok {
           // @concept: inertness (sanctioned attribute-value read site)
           attrsMatcher, _ := v.(map[string]any)
           for path, want := range attrsMatcher {
               got, found := walkAttrPath(ctx.AttributeBag, path)
               if !found {
                   return false
               }
               if !primitiveEqual(got, want) {
                   return false
               }
           }
       }
       return true
   }

   // walkAttrPath walks a dotted path through bag and returns the leaf
   // value plus whether the path resolved. Returns (nil, false) for any
   // non-map intermediate.
   func walkAttrPath(bag map[string]any, path string) (any, bool) {
       cur := any(bag)
       parts := strings.Split(path, ".")
       for _, p := range parts {
           m, ok := cur.(map[string]any)
           if !ok {
               return nil, false
           }
           v, exists := m[p]
           if !exists {
               return nil, false
           }
           cur = v
       }
       return cur, true
   }

   // primitiveEqual compares two values for equality. Type-coerces
   // numeric values across float64 / int / int64 / json.Number per the
   // existing by_match validator + runtime convention. Returns false
   // when either side is non-primitive.
   func primitiveEqual(a, b any) bool {
       // Reduce json.Number on either side to float64 for the numeric
       // branches.
       if n, ok := a.(json.Number); ok {
           if f, err := n.Float64(); err == nil {
               a = f
           }
       }
       if n, ok := b.(json.Number); ok {
           if f, err := n.Float64(); err == nil {
               b = f
           }
       }
       switch av := a.(type) {
       case string:
           bv, ok := b.(string)
           return ok && av == bv
       case bool:
           bv, ok := b.(bool)
           return ok && av == bv
       case float64:
           switch bv := b.(type) {
           case float64:
               return av == bv
           case int:
               return av == float64(bv)
           case int64:
               return av == float64(bv)
           }
           return false
       case int:
           switch bv := b.(type) {
           case float64:
               return float64(av) == bv
           case int:
               return av == bv
           case int64:
               return int64(av) == bv
           }
           return false
       case int64:
           switch bv := b.(type) {
           case float64:
               return float64(av) == bv
           case int:
               return av == int64(bv)
           case int64:
               return av == bv
           }
           return false
       }
       return false
   }
   ```

3. The `entryIndex int` parameter on `Evaluate` exists for the warn-log message (so an operator can find the offending entry in a `by_match` list). For breakpoint callers (single matcher per row, not a list), pass `0`; the warn log is still useful for forensics.

**Verification:** `go build ./foundation/matcher/...` exits 0.

### Task 8: Create `foundation/matcher/validate.go`

**Files:** `foundation/matcher/validate.go` (new)

**Steps:**
1. Read `control/controlapi/attribute_overrides.go::validateMatcherKeys` in full (around line 195 onward).

2. Create `foundation/matcher/validate.go`:

   ```go
   // Copyright © 2026 Fall Guy Consulting. ...

   package matcher

   import (
       "encoding/json"
       "fmt"
       "strings"

       "github.com/fallguyconsulting/rimsky/foundation/shared"
   )

   // ValidationRefs supplies the reference name-sets and policy flags
   // the validator uses. Most fields are optional; when a set is nil
   // the corresponding cross-check is skipped.
   //
   // The by_match wire-shape validator supplies all fields including
   // UsedExecutors and LegacyFlat to preserve the existing behavior
   // (executor must be referenced by some template node; the "graph:"
   // matcher key is rejected when the template has no declared
   // sub-graphs). The breakpoint validator supplies NodeTypes,
   // ExecutorNames, and GraphNames; leaves UsedExecutors=nil (no such
   // constraint for breakpoints) and LegacyFlat=false (breakpoints
   // accept "graph:" on any template).
   type ValidationRefs struct {
       NodeTypes     map[string]struct{} // when non-nil, node_type must be a member
       ExecutorNames map[string]struct{} // when non-nil, executor must be a member
       UsedExecutors map[string]struct{} // when non-nil, executor must additionally be referenced by some template node (by_match-specific)
       GraphNames    map[string]struct{} // when non-nil, graph must be a member (typically "main" plus declared sub-graphs)
       LegacyFlat    bool                // when true, the "graph:" matcher key is rejected entirely (legacy template with no declared sub-graphs)
   }

   // Validate enforces the matcher's grammar at registration time.
   // Returns nil on success or an error wrapping ErrInvalid.
   //
   // Validation rules (per spec 2026-05-21-attribute-overrides-matcher-overlay-design
   // and 2026-05-24-instance-debugger-design):
   //
   //   - Unknown matcher keys rejected.
   //   - Ordinal-shaped keys (dispatch_index, nth_child, partition_index, seq) rejected.
   //   - child_key MUST be a non-empty string (empty string is the
   //     non-fan-out sentinel).
   //   - node_type, executor, graph values cross-checked against the
   //     refs sets when supplied.
   //   - attrs values must be primitives.
   //
   // The entryIndex parameter is the matcher's position in an outer
   // list (for by_match's per-entry error messages). Callers without
   // a list (e.g., breakpoint creation) pass -1 to suppress the
   // "[N]" prefix.
   func Validate(m Matcher, refs ValidationRefs, entryIndex int) error {
       prefix := ""
       if entryIndex >= 0 {
           prefix = fmt.Sprintf("[%d]", entryIndex)
       }
       wrap := func(format string, args ...any) error {
           msg := fmt.Sprintf(format, args...)
           return shared.Wrap(ErrInvalid, "matcher"+prefix+": "+msg, nil)
       }

       // Reject ordinal-shaped keys with a redirect message.
       ordinals := []string{"dispatch_index", "nth_child", "partition_index", "seq"}
       for _, k := range ordinals {
           if _, ok := m[k]; ok {
               return wrap("ordinal key %q rejected — use child_key for per-partition routing or attrs.<path> for attribute-based routing", k)
           }
       }

       for k, v := range m {
           if _, ok := allowedKeys[k]; !ok {
               return wrap("unknown matcher key %q (allowed: node_type, executor, graph, child_key, attrs)", k)
           }
           switch k {
           case "node_type":
               s, ok := v.(string)
               if !ok || s == "" {
                   return wrap("matcher.node_type must be a non-empty string")
               }
               if refs.NodeTypes != nil {
                   if _, known := refs.NodeTypes[s]; !known {
                       return wrap("matcher.node_type %q is not a declared node type", s)
                   }
               }
           case "executor":
               s, ok := v.(string)
               if !ok || s == "" {
                   return wrap("matcher.executor must be a non-empty string")
               }
               if refs.ExecutorNames != nil {
                   if _, known := refs.ExecutorNames[s]; !known {
                       return wrap("matcher.executor %q is not a declared executor", s)
                   }
               }
               if refs.UsedExecutors != nil {
                   if _, used := refs.UsedExecutors[s]; !used {
                       return wrap("matcher.executor %q is declared but not referenced by any template node", s)
                   }
               }
           case "graph":
               s, ok := v.(string)
               if !ok || s == "" {
                   return wrap("matcher.graph must be a non-empty string (\"main\" or a declared sub-graph name)")
               }
               if refs.LegacyFlat {
                   // Legacy flat templates (no declared sub-graphs) accept
                   // only "main"; other graph names are not declarable.
                   // Preserves existing by_match validator behavior.
                   if s != "main" {
                       return wrap("matcher.graph %q is not admissible for legacy flat templates (no declared sub-graphs); only \"main\" is accepted", s)
                   }
               }
               if refs.GraphNames != nil {
                   if _, known := refs.GraphNames[s]; !known {
                       return wrap("matcher.graph %q (must be \"main\" or a declared sub-graph name)", s)
                   }
               }
           case "child_key":
               s, ok := v.(string)
               if !ok || s == "" {
                   return wrap("matcher.child_key must be a non-empty string (empty string is the non-fan-out sentinel, not a matcher target)")
               }
           case "attrs":
               attrs, ok := v.(map[string]any)
               if !ok {
                   return wrap("matcher.attrs must be an object")
               }
               for path, want := range attrs {
                   if !isPrimitive(want) {
                       return wrap("matcher.attrs.%s must be a primitive (string, bool, number); got %T", path, want)
                   }
                   if strings.TrimSpace(path) == "" {
                       return wrap("matcher.attrs key must be a non-empty dotted path")
                   }
               }
           }
       }
       return nil
   }

   // isPrimitive returns true if v is a JSON primitive (string, bool,
   // number including json.Number).
   func isPrimitive(v any) bool {
       switch v.(type) {
       case string, bool, float64, int, int64:
           return true
       case json.Number:
           return true
       }
       return false
   }
   ```

3. The `entryIndex int` parameter mirrors `Evaluate`'s. Callers that don't have a list index (e.g., breakpoint creation with a single matcher) pass `-1`, suppressing the `[N]` prefix in the error message.

**Verification:** `go build ./foundation/matcher/...` exits 0.

### Task 9: Write tests for `foundation/matcher/`

**Files:**
- `foundation/matcher/matcher_test.go` (new)
- `foundation/matcher/validate_test.go` (new)

**Steps:**
1. The existing tests at `runtime/attribute_overrides_test.go` exercise `applyAttributeOverrides` (the outer function), not `evaluateMatcher` directly. Those tests stay where they are — Task 10 preserves `evaluateMatcher` as a thin delegate, so the existing tests continue to pass against the new shape.
2. Write NEW direct-call tests in `foundation/matcher/matcher_test.go` for `matcher.Evaluate` covering:
   - All 5 matcher keys (`node_type`, `executor`, `graph`, `child_key`, `attrs`) — happy path matches and mismatches.
   - Empty matcher fires on every dispatch.
   - Unknown keys cause defensive-skip (return false) with a Warn log.
   - Primitive-equality coercion across `int` / `int64` / `float64` / `json.Number`.
   - Attribute-path walking through nested maps; missing-path mismatch.
3. Write NEW direct-call tests in `foundation/matcher/validate_test.go` for `matcher.Validate` covering:
   - Unknown-key rejection.
   - Ordinal-key rejection (`dispatch_index`, `nth_child`, `partition_index`, `seq`).
   - `child_key: ""` rejection.
   - Cross-checks against `NodeTypes`, `ExecutorNames`, `UsedExecutors`, `GraphNames`.
   - `LegacyFlat=true` accepts only `graph: "main"`, rejects others.
   - `entryIndex = -1` produces error messages without the `[N]` prefix; `entryIndex = 3` produces messages with `[3]` prefix.
4. The existing validator tests at `control/controlapi/attribute_overrides_test.go` continue to exercise the by_match wrapper end-to-end (via `validateAttributeOverrides`). They keep using `errors.Is(err, errAttributeOverridesInvalid)` because Task 11 re-wraps `matcher.ErrInvalid` results in `errAttributeOverridesInvalid` at the wrapper boundary.

**Verification:** `go test ./foundation/matcher/... ./runtime/... ./control/controlapi/... -count=1`.

### Task 10: Update `runtime/attribute_overrides.go` to delegate to `foundation/matcher/`

**Files:** `runtime/attribute_overrides.go` (modify)

**Steps:**
1. Add `"github.com/fallguyconsulting/rimsky/foundation/matcher"` to the imports.
2. Replace the body of `evaluateMatcher` with a delegation:
   ```go
   func evaluateMatcher(
       m map[string]any,
       executor, nodeName, graph, childKey string,
       bag map[string]any,
       logger shared.Logger,
       entryIndex int,
   ) bool {
       return matcher.Evaluate(matcher.Matcher(m), matcher.Context{
           Executor:     executor,
           NodeType:     nodeName,
           Graph:        graph,
           ChildKey:     childKey,
           AttributeBag: bag,
       }, logger, entryIndex)
   }
   ```
3. Delete the now-unused local helpers in this file: `matcherAllowedKeys` (the var), `walkAttrPath`, `primitiveEqual`. They live in the matcher package now.
4. The `@concept: inertness` annotation that previously sat at the `attrs` branch inside `evaluateMatcher` is now in the matcher package. The wrapper at `runtime/attribute_overrides.go` no longer needs the annotation — but `applyAttributeOverrides` itself stays annotated `@concept: attribute` as before. Keep that.

**Verification:** `go test ./runtime/... -count=1`.

### Task 11: Update `control/controlapi/attribute_overrides.go::validateMatcherKeys` to delegate

**Files:** `control/controlapi/attribute_overrides.go` (modify)

**Steps:**
1. Add `"github.com/fallguyconsulting/rimsky/foundation/matcher"` to the imports.
2. The existing `validateMatcherKeys` signature is:
   ```go
   func validateMatcherKeys(
       entryIdx int,
       matcher map[string]any,
       nodeNames, usedExecutors map[string]struct{},
       executors map[string]ExecutorEntry,
       graphNames map[string]struct{},
       legacyFlat bool,
   ) error
   ```
   The `executors map[string]ExecutorEntry` parameter is a `control/controlapi`-internal typed map. `foundation/matcher` cannot import `ExecutorEntry` (back-cycle). Project the executor names to a `map[string]struct{}` at the call site:
   ```go
   func validateMatcherKeys(
       entryIdx int,
       matcherMap map[string]any,
       nodeNames, usedExecutors map[string]struct{},
       executors map[string]ExecutorEntry,
       graphNames map[string]struct{},
       legacyFlat bool,
   ) error {
       execNames := make(map[string]struct{}, len(executors))
       for name := range executors {
           execNames[name] = struct{}{}
       }
       return matcher.Validate(matcher.Matcher(matcherMap), matcher.ValidationRefs{
           NodeTypes:     nodeNames,
           ExecutorNames: execNames,
           UsedExecutors: usedExecutors,
           GraphNames:    graphNames,
           LegacyFlat:    legacyFlat,
       }, entryIdx)
   }
   ```
   Rename the function's `matcher` parameter to `matcherMap` to avoid shadowing the imported package.
3. To preserve existing tests that assert `errors.Is(err, errAttributeOverridesInvalid)` (`control/controlapi/attribute_overrides_test.go:181` and `:559`), the delegated `matcher.Validate` error must wrap to the existing sentinel. Update Task 11's body to do this:

   ```go
   if err := matcher.Validate(matcher.Matcher(matcherMap), matcher.ValidationRefs{
       NodeTypes:     nodeNames,
       ExecutorNames: execNames,
       UsedExecutors: usedExecutors,
       GraphNames:    graphNames,
       LegacyFlat:    legacyFlat,
   }, entryIdx); err != nil {
       // Re-wrap to preserve the existing errAttributeOverridesInvalid sentinel
       // so by_match's existing test assertions and HTTP-status translation
       // continue to work unchanged.
       return shared.Wrap(errAttributeOverridesInvalid, err.Error(), nil)
   }
   return nil
   ```
   The breakpoint code path (Task 20's handler) checks `errors.Is(err, matcher.ErrInvalid)` directly — it doesn't go through `errAttributeOverridesInvalid`. The two sentinels live in different packages and have different translation sites (`control/controlapi/app.go::writeError` translates both to 400).

**Verification:** `go test ./control/controlapi/... -count=1 && go build ./...`.

---

## Pass 3: Persistence layer impls

**Goal:** Replace the stubs from Pass 1 Task 6 with real implementations for BreakpointTable and BreakpointHitTable across both backends.

**Scope:** Tasks 12–15
**End state:** working
**Verification:** `go build ./... && go test ./foundation/persistence/... -count=1 && make lint`

### Task 12: Implement `foundation/persistence/postgres/breakpoints.go`

**Files:** `foundation/persistence/postgres/breakpoints.go` (replace stubs)

**Steps:**
1. Read sibling implementations for shape — `foundation/persistence/postgres/instances.go` is the closest pattern. It declares methods on the aspect type (e.g., `func (b *instancesImpl) Get(...) {...}`), uses `b.q(tx)` to obtain the querier (which **panics on nil tx** — every method must require a non-nil `persistence.Tx`), and marshals JSONB via `json.Marshal` / `Unmarshal`.
2. Replace each stubbed method on `*breakpointsImpl` with a real impl:
   - **Create**: INSERT into `rimsky_instance_breakpoints`, RETURNING `id`. Materialize `expires_at = NOW() + ($N || ' seconds')::interval` when `ttl_seconds IS NOT NULL`; otherwise leave NULL. Marshal `matcher` map → JSONB.
   - **Get**: SELECT a single row by `id`. Return `(nil, nil)` for "not found" — `errors.Is(err, pgx.ErrNoRows)` → `(nil, nil)` is the codebase pattern. Unmarshal `matcher` JSONB → map.
   - **ListForInstance**: SELECT with an optional `AND (expires_at IS NULL OR expires_at > NOW())` filter when `includeExpired = false`.
   - **Delete**: simple DELETE by `id`. Cascades to `rimsky_breakpoint_hits` via the FK ON DELETE CASCADE; no manual cleanup needed.
   - **IncrementDropped**: `UPDATE rimsky_instance_breakpoints SET dropped_count = dropped_count + 1 WHERE id = $1`.
   - **SweepExpired**: `DELETE FROM rimsky_instance_breakpoints WHERE expires_at IS NOT NULL AND expires_at <= $1`. Return the rowcount via `CommandTag.RowsAffected()`.
3. Every method's first line is the `b.q(tx)` querier acquisition. Passing `tx = nil` panics with the project-wide message — callers MUST supply a real transaction via `Tables.Transaction(...)`.

**Verification:** `go build ./foundation/persistence/postgres/...`.

### Task 13: Implement `foundation/persistence/postgres/breakpoint_hits.go`

**Files:** `foundation/persistence/postgres/breakpoint_hits.go` (replace stubs)

**Steps:**
1. Methods are on `*breakpointHitsImpl`. Same q(tx) discipline as Task 12 — non-nil tx required.
2. **Create**: INSERT into `rimsky_breakpoint_hits`, `RETURNING id, seq`. Returns both UUID and int64. Marshal `snapshot` map → JSONB.
3. **Get**: SELECT by `id` (the UUID column, not seq). Return `(nil, nil)` on not-found.
4. **ListSinceForInstance**: `SELECT * FROM rimsky_breakpoint_hits WHERE instance_id = $1 AND seq > $2 ORDER BY seq ASC LIMIT $3`. Returns all hits (resumed or not) — the cursor pages through every hit; the agent inspects `resumed_at` on each row to know its state.
5. **ListSinceForBreakpoint**: same shape but filtered by `breakpoint_id`.
6. **ListUnresumedForBreakpoint**: `SELECT * FROM rimsky_breakpoint_hits WHERE breakpoint_id = $1 AND resumed_at IS NULL ORDER BY hit_at ASC`.
7. **Resume**: `UPDATE rimsky_breakpoint_hits SET resumed_at = NOW(), resumed_by_key = $2, resume_overlay = $3 WHERE id = $1 AND resumed_at IS NULL`. On 0 rows affected, check if the row exists (replay vs. truly missing):
   - Read `resumed_at` of the row. If `IS NOT NULL` → idempotent replay, return nil (no state change).
   - If row doesn't exist → return `shared.ErrBreakpointHitNotFound`.
8. **AutoResumeStale**: `UPDATE rimsky_breakpoint_hits h SET resumed_at = $1, resumed_by_key = 'sweeper' FROM rimsky_instance_breakpoints b WHERE h.breakpoint_id = b.id AND h.resumed_at IS NULL AND b.overflow_policy = 'auto_resume_after_ttl' AND h.hit_at + (b.hit_ttl_seconds || ' seconds')::interval <= $1`. The `FROM ... WHERE` join syntax is Postgres-specific. Return rowcount.
9. **DropOldest(bpID, keepCount)**: DELETE the oldest unresumed hits beyond `keepCount` for the breakpoint:
   ```sql
   DELETE FROM rimsky_breakpoint_hits
   WHERE seq IN (
     SELECT seq FROM rimsky_breakpoint_hits
     WHERE breakpoint_id = $1 AND resumed_at IS NULL
     ORDER BY seq ASC
     LIMIT GREATEST(0, (SELECT COUNT(*) FROM rimsky_breakpoint_hits WHERE breakpoint_id = $1 AND resumed_at IS NULL) - $2)
   );
   ```
   Returns the rowcount of deleted (oldest, unresumed) rows.
10. **UnresumedCount**: `SELECT COUNT(*) FROM rimsky_breakpoint_hits WHERE breakpoint_id = $1 AND resumed_at IS NULL`.

**Verification:** `go build ./foundation/persistence/postgres/...`.

### Task 14: Implement SQLite counterparts and add new error sentinels

**Files:**
- `foundation/persistence/sqlite/breakpoints.go` (replace stubs)
- `foundation/persistence/sqlite/breakpoint_hits.go` (replace stubs)
- `foundation/shared/errors.go` (modify — add new error sentinels following the existing `var ErrXxx = errors.New("...")` pattern)
- `control/controlapi/app.go` (modify — extend `writeError` at line 284 to map the new sentinels)

**Steps:**
1. Mirror Tasks 12 and 13 for the SQLite backend (impls on the SQLite aspect types declared in Pass 1 Task 6). SQLite-specific syntax notes:
   - UUID default expression: use the existing SQLite UUID-default expression from `foundation/persistence/sqlite/migrations/001-schema.sql` verbatim (the same expression used for `gen_random_uuid()` columns elsewhere).
   - `RETURNING` is supported in modernc.org/sqlite for INSERT / UPDATE / DELETE.
   - `NOW()` becomes `CURRENT_TIMESTAMP`.
   - Interval arithmetic for `expires_at` materialization: `datetime('now', $N || ' seconds')` where $N is the ttl_seconds bind.
   - `auto_resume_after_ttl` join in `AutoResumeStale`: SQLite supports `UPDATE ... FROM` since 3.33, modernc.org/sqlite ships a current SQLite; if the syntax is awkward, use a sub-SELECT instead.
2. Add the new error sentinels to `foundation/shared/errors.go` in the existing `var ( ... )` block, following the pattern `ErrTemplateValidation = errors.New(...)`:
   ```go
   ErrBreakpointNotFound    = errors.New("breakpoint not found")
   ErrBreakpointHitNotFound = errors.New("breakpoint hit not found")
   ErrResumeOverlayInvalid  = errors.New("resume overlay invalid")
   ErrInstanceNotPaused     = errors.New("instance not paused")
   ErrInstanceAlreadyPaused = errors.New("instance already paused")
   ```
   Note: `ErrMatcherInvalid` is **not** added to shared — it lives in `foundation/matcher` as `matcher.ErrInvalid` (declared in Task 7). The control-api translates it the same way (400) via the route taken in Task 11.
3. In `control/controlapi/app.go::writeError` (line 284), extend the existing `switch`/`errors.Is` chain to translate the new sentinels:
   - `errors.Is(err, shared.ErrBreakpointNotFound)` → 404
   - `errors.Is(err, shared.ErrBreakpointHitNotFound)` → 404
   - `errors.Is(err, shared.ErrResumeOverlayInvalid)` → 400
   - `errors.Is(err, shared.ErrInstanceNotPaused)` → 409
   - `errors.Is(err, shared.ErrInstanceAlreadyPaused)` → 409
   - `errors.Is(err, matcher.ErrInvalid)` → 400 (already added in Task 11 for the by_match path; the new breakpoint handlers reuse it)

**Verification:** `go test ./foundation/persistence/sqlite/... ./control/controlapi/... -count=1`.

### Task 15: Write persistence-layer tests

**Files:**
- `foundation/persistence/postgres/breakpoints_test.go` (new)
- `foundation/persistence/sqlite/breakpoints_test.go` (new) (one combined test file per backend exercising both tables is fine; mirror the existing per-backend test file conventions, e.g., `foundation/persistence/postgres/instances_test.go`)

**Steps:**
1. Mirror the testcontainers / in-memory setup used by sibling tests (e.g., `instances_test.go`).
2. Cover at minimum:
   - Create + Get round-trip for both tables.
   - ListForInstance with includeExpired=true and =false (creates one expired and one active breakpoint).
   - IncrementDropped is monotonic and tx-safe.
   - SweepExpired only deletes past-expiry rows.
   - For hits: Create returns both id and seq; seq is monotonic.
   - ListSinceForInstance and ListSinceForBreakpoint return rows in seq-ascending order, exclude resumed-only rows when... wait, actually they should INCLUDE resumed rows because the cursor is for "all hits ever" not "unresumed hits." Confirm: the `since` cursor pages through every hit; resumed status is in the row's data. Adjust the SELECT in Task 13 accordingly — no `WHERE resumed_at IS NULL` filter on these list methods.
   - ListUnresumedForBreakpoint returns only unresumed rows.
   - Resume sets `resumed_at`, `resumed_by_key`, `resume_overlay`. Replay (second call) returns nil error and leaves the row unchanged.
   - AutoResumeStale resumes only past-TTL unresumed rows.
   - DropOldest with keepCount=99 against 150 unresumed rows leaves the 99 most-recent.
   - UnresumedCount returns the right value.
3. Run race-mode tests on the postgres backend (testcontainers tests support `-race`):
   `go test ./foundation/persistence/postgres/... -count=3 -race`.

**Verification:** `go test ./foundation/persistence/... -count=1 && go test ./foundation/persistence/postgres/... -count=3 -race`.

---

## Pass 4: Control-API routes + action registry + role-template + paused-on-create

**Goal:** Land the HTTP endpoints for breakpoint CRUD, instance pause/resume, and the paused-on-create affordance. Register new action verbs in `v1Actions`. Create the `debug-operator` role-template.

**Scope:** Tasks 16–22
**End state:** working
**Verification:** `go test ./control/controlapi/... -count=1 && make lint`

### Task 16: Add action verbs to `v1Actions`

**Files:** `control/controlapi/actions.go` (modify)

**Steps:**
1. Locate the `v1Actions` slice in `control/controlapi/actions.go`. The existing entries follow the pattern:
   ```go
   {Action: "instance:read", IsWrite: false, ...},
   ```
2. Add the following entries, in the appropriate alphabetic / logical positions:
   - `{Action: "instance:pause",  IsWrite: true,  ...}`
   - `{Action: "instance:resume", IsWrite: true,  ...}`
   - `{Action: "breakpoint:read",   IsWrite: false, ...}`
   - `{Action: "breakpoint:create", IsWrite: true,  ...}`
   - `{Action: "breakpoint:resume", IsWrite: true,  ...}`
   - `{Action: "breakpoint:delete", IsWrite: true,  ...}`
3. Each entry includes the route-to-action mapping fields per `code:control/controlapi/actions.go::ActionEntry`. The struct's fields are `Action`, `IsWrite`, `Routes []Route`, `MCPTools []string`, and `Description`. Read a neighboring entry like `instance:read` to see the exact shape.

**Verification:** `go test ./control/controlapi/... -count=1 -run TestActions` (or whatever the existing v1Actions registry test is named — find it via `grep -n "v1Actions" control/controlapi/*_test.go`).

### Task 17: Add `paused` field to `createInstanceRequest` and update the handler

**Files:**
- `control/controlapi/instances.go` (modify — request shape + handler)
- `foundation/persistence/instances.go` (modify — add Paused to InstanceRow and to InstanceCreateInput)
- `foundation/persistence/postgres/instances.go` (modify — extend the `instanceCols` constant, the SELECT scan list, and the INSERT bind set)
- `foundation/persistence/sqlite/instances.go` (modify — same for SQLite)

**Steps:**
1. In `foundation/persistence/instances.go`, add `Paused bool` to:
   - The `InstanceRow` struct (the read projection).
   - The `InstanceCreateInput` struct (the write projection — separate struct used by `Create`).
2. In `foundation/persistence/postgres/instances.go`:
   - Locate `const instanceCols` (around line 29). Add `paused` to the column list (in dependency-ordered position, e.g., after `frame_delivery_mode`).
   - Find every SELECT that uses `instanceCols` and the corresponding `Scan(...)` call — add `&row.Paused` in the same position.
   - Find the INSERT path inside `instancesImpl.Create` (or wherever instances are inserted). Add the `paused` column and its bind parameter; pass `input.Paused`.
3. Mirror the SQLite-side changes in `foundation/persistence/sqlite/instances.go`.
4. In `control/controlapi/instances.go`'s `createInstanceRequest` struct (around line 92), add:
   ```go
   // Paused is the create-time hold flag. When true, the instance is
   // created with rimsky_instances.paused = true; the supervisor's
   // candidate-selection skips it until POST /instances/{id}/resume
   // releases the hold. Per concept:instance.
   Paused bool `json:"paused,omitempty"`
   ```
5. In `handleCreateInstance`, find where `InstanceCreateInput` (or the row struct used by the persistence Create) is constructed from the request body. Add `Paused: body.Paused`.
6. Existing tests that construct `InstanceCreateInput` may need to set `Paused: false` explicitly (or rely on the zero value — `bool`'s zero value is `false` which is the existing behavior).

**Verification:** `go test ./foundation/persistence/... ./control/controlapi/... -count=1 -run TestCreateInstance`.

### Task 18: Add `POST /instances/{id}/pause` and `POST /instances/{id}/resume` handlers

**Files:** `control/controlapi/instances.go` (modify — add handlers and route registrations)

**Steps:**
1. In `registerInstancesRoutes`, after the existing instance routes, add:
   ```go
   r.Post("/instances/{idOrKey}/pause",  gate(deps, "instance:pause",  handlePauseInstance(deps)))
   r.Post("/instances/{idOrKey}/resume", gate(deps, "instance:resume", handleResumeInstance(deps)))
   ```
2. Implement `handlePauseInstance`:
   - Resolve `{idOrKey}` to a UUID (use the existing helper if present, e.g., `resolveInstance`).
   - Inside a transaction: SELECT the row's current `paused` column.
   - If `paused = true`, return 409 with `shared.ErrInstanceAlreadyPaused`.
   - Otherwise UPDATE to `paused = true` and return `{"paused": true}` with 200.
3. Implement `handleResumeInstance` symmetrically:
   - If `paused = false`, return 409 with `shared.ErrInstanceNotPaused`.
   - Otherwise UPDATE to `paused = false` and return `{"resumed": true}` with 200.
4. Add a persistence-layer method `SetPaused(ctx, instanceID, paused bool, tx) (priorValue bool, err error)` on InstanceTable. Implement on both backends. The handler reads the prior value to determine whether to return 409 (no change required) or 200 (toggled).

**Verification:** `go test ./control/controlapi/... -count=1 -run TestPauseInstance -run TestResumeInstance`.

### Task 19: Update supervisor candidate-selection to skip paused instances

**Files:**
- `foundation/persistence/postgres/queue.go` (modify — `SelectCandidates` at line 172)
- `foundation/persistence/sqlite/queue.go` (modify — same function)

**Steps:**
1. Read `foundation/persistence/postgres/queue.go::SelectCandidates` (around line 172). The current query joins `rimsky_nodes n` (which has `instance_id`) and `rimsky_node_runs d` (which does NOT have `instance_id`). The candidate row's table is `rimsky_node_runs`, so the join to `rimsky_instances` must go through `rimsky_nodes n`, not through the candidate row directly.
2. Add `JOIN rimsky_instances i ON i.id = n.instance_id` to the FROM clause, and add `AND i.paused = false` to the WHERE clause.
3. Verify the existing column references in SELECT and WHERE still resolve unambiguously after the new join (use table-qualified names if needed).
4. Mirror the SQLite version — same JOIN through `rimsky_nodes`.
5. Add a persistence-level test in `foundation/persistence/postgres/queue_test.go` (or wherever `SelectCandidates` is tested) that creates one paused and one unpaused instance with eligible work, runs `SelectCandidates`, and asserts only the unpaused instance's work is returned. Mirror for SQLite.

**Verification:** `go test ./foundation/persistence/... -count=1 -run TestSelectCandidates`.

### Task 20: Create `control/controlapi/breakpoints.go` with all six endpoints + the runtime resume helper

**Files:**
- `runtime/breakpoint_resume.go` (new — the resume-overlay validation + persistence helper)
- `runtime/breakpoint_resume_test.go` (new)
- `control/controlapi/breakpoints.go` (new — thin HTTP handlers)
- `control/controlapi/breakpoints_test.go` (new)
- `control/controlapi/app.go` (modify — register the new route group)

**Background on the resume helper:** Resume-overlay validation is domain logic (it interprets the hit's snapshot, merges against the executor's effective schema, persists the validated overlay). Putting it in the HTTP handler would mix transport and domain. Extracting to `runtime/breakpoint_resume.go` keeps the HTTP handler thin (parse body → call helper → translate errors) and gives any future transport (MCP-shape, webhook, SSE) the same single entry point. See spec §11 separation-of-concerns.

**Steps:**
1. Create `runtime/breakpoint_resume.go`:
   ```go
   // Copyright © 2026 Fall Guy Consulting. ...

   package runtime

   import (
       "context"
       "errors"

       "github.com/fallguyconsulting/rimsky/foundation/persistence"
       "github.com/fallguyconsulting/rimsky/foundation/shared"
       "github.com/fallguyconsulting/rimsky/graph/attribute" // for Validate / effective-schema helpers
   )

   // ResumeResult reports whether the resume call was the first one
   // for this hit (true) or an idempotent replay (false). Returned to
   // the HTTP / MCP transport so they can shape the wire response
   // consistently.
   type ResumeResult struct {
       FirstResume bool
   }

   // ValidateAndPersistResume implements the resume-time validation
   // discipline from spec §4.7:
   //   1. Fetch the hit. Return ErrBreakpointHitNotFound on 404.
   //   2. If already resumed, return ResumeResult{FirstResume: false}.
   //   3. If overlay present, validate shape (delegated to caller),
   //      merge against hit.Snapshot.dispatch_context.merged_attributes,
   //      and run attributes.Validate against the executor's effective
   //      schema. Return ErrResumeOverlayInvalid on failure.
   //   4. Persist the validated overlay + resumed_at + resumed_by_key.
   //
   // @concept: breakpoint
   func ValidateAndPersistResume(
       ctx context.Context,
       args RunArgs,
       hitID shared.UUID,
       overlay map[string]any,
       byKey string,
   ) (*ResumeResult, error) {
       var result ResumeResult
       err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
           hit, err := args.Persist.BreakpointHits().Get(ctx, hitID, tx)
           if err != nil { return err }
           if hit == nil {
               return shared.ErrBreakpointHitNotFound
           }
           if hit.ResumedAt != nil {
               result.FirstResume = false
               return nil // idempotent replay
           }
           if overlay != nil {
               // Read the post-L5 bag the snapshot captured.
               snapDC, _ := hit.Snapshot["dispatch_context"].(map[string]any)
               merged, _ := snapDC["merged_attributes"].(map[string]any)
               postOverlay := shared.DeepMergeJSON(merged, overlay).(map[string]any)
               // Look up the executor's effective schema for the dispatch.
               // The exact accessor depends on the resolved schema cache;
               // implementer follows the precedent at code:runtime/runner_dispatch.go
               // for how executor schemas are read.
               schema, err := lookupEffectiveSchemaForHit(ctx, args, hit, tx)
               if err != nil { return err }
               if err := attribute.Validate(schema, postOverlay, attribute.PhaseDispatch); err != nil {
                   return shared.Wrap(shared.ErrResumeOverlayInvalid, err.Error(), nil)
               }
           }
           if err := args.Persist.BreakpointHits().Resume(ctx, hitID, byKey, overlay, tx); err != nil {
               return err
           }
           result.FirstResume = true
           return nil
       })
       return &result, err
   }
   ```
   The `lookupEffectiveSchemaForHit` helper is private to this file; the implementer wires it against the existing schema-resolution path in `runtime/runner_dispatch.go`. If reusing that path requires substantial refactor, an acceptable fallback is to store the effective schema reference in the snapshot at hit-write time (Task 23's `buildSnapshot`) so resume-time validation has it inline.

2. Write `runtime/breakpoint_resume_test.go` covering: 404 on missing hit; idempotent replay returns `FirstResume: false`; valid overlay merges + validates + persists; invalid overlay returns `ErrResumeOverlayInvalid`.

3. Create `control/controlapi/breakpoints.go` with:
   - `registerBreakpointsRoutes(r chi.Router, deps AppDeps)` registering the 6 routes:
     ```go
     r.Post("/instances/{idOrKey}/breakpoints",                                 gate(deps, "breakpoint:create", handleCreateBreakpoint(deps)))
     r.Get("/instances/{idOrKey}/breakpoints",                                  gate(deps, "breakpoint:read",   handleListBreakpoints(deps)))
     r.Delete("/instances/{idOrKey}/breakpoints/{breakpoint_id}",               gate(deps, "breakpoint:delete", handleDeleteBreakpoint(deps)))
     r.Post("/instances/{idOrKey}/breakpoints/{breakpoint_id}/resume",          gate(deps, "breakpoint:resume", handleResumeBreakpointHit(deps)))
     ```
   - Note: the spec uses `POST /instances/{id}/breakpoints/{breakpoint_id}/resume` (resume a hit, body carries `hit_id` + optional `overlay`). The route is on the breakpoint resource per the spec; the body carries the hit identity.

4. Implement each handler per spec §4.1 (CRUD), §4.7 (resume). Handlers stay thin — they parse JSON, validate transport-level shape, and call into persistence / matcher / runtime helpers.
5. Handler details:
   - `handleCreateBreakpoint`: parse body, apply mode-conditional defaults to `overflow_policy` if absent (`""` → `OverflowDropOldest` when mode is `notify_only`; `""` → `OverflowBlockDispatch` when mode is `pause`; the schema column is `NOT NULL` with no SQL default since the default depends on mode), call `matcher.Validate` with the instance's template's NodeTypes / Executors / GraphNames refs (read from the locked template row), reject invalid `(mode, overflow_policy)` combos (`BreakpointModePause`+`OverflowDropOldest`, `BreakpointModeNotifyOnly`+`OverflowBlockDispatch`) after defaulting, validate `signal_type` is NULL on `CheckpointBeforeDispatch` and conforms to `signal.ValidateSubscriptionType` (trailing-`*` admitted) on `CheckpointAfterTerminal`, then insert via `BreakpointTable.Create`. Return 201 with `{breakpoint_id, ...echoed...}`. References the typed constants from Task 5.
   - `handleListBreakpoints`: call `BreakpointTable.ListForInstance` with `includeExpired=false`, return projection.
   - `handleDeleteBreakpoint`: call `BreakpointTable.Delete`. Hits cascade via FK.
   - `handleResumeBreakpointHit`: thin — parse body `{hit_id, overlay?}`, then call `runtime.ValidateAndPersistResume(ctx, runArgs, hitID, overlay, requestingKeyID)`. Translate errors: `ErrBreakpointHitNotFound` → 404; `ErrResumeOverlayInvalid` → 400; other errors → 500. On success, return 200 with `{"resumed": true, "first_resume": result.FirstResume}`.

6. In `control/controlapi/app.go`, locate the section that calls `registerInstancesRoutes`, `registerTemplatesRoutes`, etc. Add a call to `registerBreakpointsRoutes(r, deps)`.

7. Write `control/controlapi/breakpoints_test.go` covering each endpoint's happy path, validation failures, and the resume-replay idempotency case. The resume-handler tests focus on the transport translation (parsing, error-status mapping); the underlying validation logic is tested in `runtime/breakpoint_resume_test.go`.

**Verification:** `go test ./runtime/... ./control/controlapi/... -count=1 -run "TestBreakpoint|TestValidateAndPersistResume"`.

### Task 21: Create the `debug-operator` role-template

**Files:** `control/cli/roles/debug-operator.json` (new)

**Steps:**
1. Read `control/cli/roles/agent-supervisor.json` for shape.
2. Create `debug-operator.json` per spec §8:
   ```json
   {
     "name": "debug-operator",
     "description": "Permission bundle for live-instance debugging: pause/resume instances and install/inspect/resume/delete runtime breakpoints. High-risk in production — grant only to operators or agent keys that need to halt or mutate live dispatches.",
     "permissions": [
       { "action": "*:read" },
       { "action": "instance:pause" },
       { "action": "instance:resume" },
       { "action": "breakpoint:create" },
       { "action": "breakpoint:resume" },
       { "action": "breakpoint:delete" }
     ]
   }
   ```
3. The CLI's role-template loader globs `control/cli/roles/*.json` per the existing convention; no separate registration step is needed.

**Verification:** `go test ./control/cli/... -count=1` (the role-template tests should auto-pick up the new file).

### Task 22: Update audit emission for the new debugger verbs

**Files:** `control/controlapi/audit.go` (verify; likely no edit required)

**Steps:**
1. Read `control/controlapi/audit.go` to confirm the existing `auth.access_attempted` / `auth.access_denied` emitter picks up the new actions automatically (it should — those events fire from the auth middleware on every gated route).
2. Confirm the audit payload includes route params and resolved IDs (instance_id, breakpoint_id, hit_id where applicable). If the existing payload construction grabs URL path params generically, no change is needed; if it lists specific fields, ensure the new path params are surfaced.
3. No new audit event kinds are added per spec §12.3.

**Verification:** `go test ./control/controlapi/... -count=1 -run TestAudit`.

---

## Pass 5: Supervisor checkpoints

**Goal:** Land the supervisor cooperation at `before_dispatch` and `after_terminal` — query breakpoints, evaluate matcher (+ signal_type for after_terminal), write hit rows, block (pause mode) or continue (notify_only). Implement `waitForResume` polling and the L6 overlay merge.

**Scope:** Tasks 23–26
**End state:** working
**Verification:** `go build ./... && go test ./runtime/... -count=1 -race && make lint`

### Task 23: Create `runtime/breakpoint_eval.go`

**Files:**
- `runtime/breakpoint_eval.go` (new)
- `runtime/breakpoint_eval_test.go` (new)

**Notes on transaction discipline:** Every persistence-layer call in this file MUST be wrapped in a `Tables.Transaction(...)` — the `q(tx)` helper in `foundation/persistence/postgres/backend.go` panics on nil tx. Long-blocking operations like `waitForResume` open a fresh short-lived tx per poll iteration; they do NOT hold a tx across the wait.

**Steps:**
1. Create `runtime/breakpoint_eval.go` with the per-checkpoint evaluator, the hit-write, the `waitForResume` poll loop, and the L6 overlay merge. Implement per the pseudocode in spec §4.2:

   ```go
   // Copyright © 2026 Fall Guy Consulting. ...

   package runtime

   import (
       "context"
       "time"

       "github.com/fallguyconsulting/rimsky/foundation/matcher"
       "github.com/fallguyconsulting/rimsky/foundation/persistence"
       "github.com/fallguyconsulting/rimsky/foundation/shared"
       signalpkg "github.com/fallguyconsulting/rimsky/foundation/signal"
   )

   const breakpointQueueCap = 100
   const breakpointResumePollInterval = 250 * time.Millisecond

   // CheckpointContext is the dispatch context the breakpoint
   // evaluator reads. DispatchID is the rimsky_node_runs.id (the
   // runtime calls this "DispatchID" everywhere; the persistence
   // column happens to be `id`); FrameID is the frame this run
   // belongs to.
   type CheckpointContext struct {
       InstanceID       shared.UUID
       DispatchID       shared.UUID  // rimsky_node_runs.id
       FrameID          shared.UUID
       Executor         string
       NodeType         string
       Graph            string
       ChildKey         string
       MergedAttributes map[string]any  // post-L5 attribute bag
       Checkpoint       string          // "before_dispatch" | "after_terminal"
       TerminalSignal   *signalpkg.Signal  // non-nil only for after_terminal
       // Snapshot inputs:
       NodeRunSnapshot   map[string]any
       HeldClaims        []map[string]any
       OpenWaitSet       []map[string]any
   }

   // EvaluateBreakpoints runs all matching breakpoints at the given
   // checkpoint. For pause-mode hits, blocks until resume. Returns
   // the (possibly-overlay-mutated) MergedAttributes for the caller
   // to use in the actual dispatch.
   //
   // Transaction discipline: every persistence call inside this
   // function opens a fresh short-lived tx via Tables.Transaction.
   // The long-blocking waitForResume polls on its own short txns —
   // no tx is held across the wait. The CALLER must NOT pass an
   // outer tx (this function is invoked outside any dispatch tx —
   // see Tasks 24 and 25).
   //
   // @concept: breakpoint
   func EvaluateBreakpoints(
       ctx context.Context,
       args RunArgs,
       cc CheckpointContext,
   ) (map[string]any, error) {
       var bps []persistence.BreakpointRow
       if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
           var err error
           bps, err = args.Persist.Breakpoints().ListForInstance(ctx, cc.InstanceID, false, tx)
           return err
       }); err != nil {
           return cc.MergedAttributes, err
       }

       result := cc.MergedAttributes
       for _, bp := range bps {
           if bp.Checkpoint != cc.Checkpoint {
               continue
           }
           if !matcher.Evaluate(matcher.Matcher(bp.Matcher), matcher.Context{
               Executor:     cc.Executor,
               NodeType:     cc.NodeType,
               Graph:        cc.Graph,
               ChildKey:     cc.ChildKey,
               AttributeBag: result,
           }, args.Logger, 0) {
               continue
           }
           if bp.SignalType != nil && cc.TerminalSignal != nil {
               if !cc.TerminalSignal.Type.HasPrefix(signalpkg.TypePath(*bp.SignalType)) {
                   continue
               }
           }

           // Pre-write overflow handling (queue cap = breakpointQueueCap).
           if err := handleOverflow(ctx, args, bp); err != nil {
               return result, err
           }

           // Write the hit row in a short tx.
           var hitID shared.UUID
           if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
               var err error
               hitID, _, err = args.Persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
                   BreakpointID: bp.ID,
                   InstanceID:   cc.InstanceID,
                   NodeRunID:    &cc.DispatchID,
                   FrameID:      &cc.FrameID,
                   Checkpoint:   cc.Checkpoint,
                   Mode:         bp.Mode,
                   Snapshot:     buildSnapshot(cc),
               }, tx)
               return err
           }); err != nil {
               return result, err
           }

           if bp.Mode == "notify_only" {
               continue
           }

           // Pause mode: block until resume.
           hit, err := waitForResume(ctx, args, hitID)
           if err != nil {
               return result, err
           }
           if hit != nil && hit.ResumeOverlay != nil {
               merged := shared.DeepMergeJSON(result, hit.ResumeOverlay).(map[string]any)
               // Defense-in-depth validation runs in the caller before
               // dispatch — this function returns the merged bag; the
               // caller routes validation failures through
               // template_validation_failed per concept:error-policy.
               result = merged
           }
       }
       return result, nil
   }

   // handleOverflow implements the per-policy cap behavior per spec §4.8.
   // Each persistence call opens its own short tx.
   func handleOverflow(ctx context.Context, args RunArgs, bp persistence.BreakpointRow) error {
       for {
           var unresumed int
           if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
               var err error
               unresumed, err = args.Persist.BreakpointHits().UnresumedCount(ctx, bp.ID, tx)
               return err
           }); err != nil {
               return err
           }
           if unresumed < breakpointQueueCap {
               return nil
           }
           switch bp.OverflowPolicy {
           case "drop_oldest":
               // notify_only only
               return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
                   if _, err := args.Persist.BreakpointHits().DropOldest(ctx, bp.ID, breakpointQueueCap-1, tx); err != nil {
                       return err
                   }
                   return args.Persist.Breakpoints().IncrementDropped(ctx, bp.ID, tx)
               })
           case "block_dispatch", "auto_resume_after_ttl":
               // Block until something drains. The sweeper handles
               // auto_resume_after_ttl drainage in the background.
               select {
               case <-ctx.Done():
                   return ctx.Err()
               case <-time.After(breakpointResumePollInterval):
               }
               continue
           }
       }
   }

   // waitForResume polls the hit row until resumed_at != NULL, opening
   // a fresh tx per poll iteration.
   func waitForResume(ctx context.Context, args RunArgs, hitID shared.UUID) (*persistence.BreakpointHitRow, error) {
       for {
           var hit *persistence.BreakpointHitRow
           if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
               var err error
               hit, err = args.Persist.BreakpointHits().Get(ctx, hitID, tx)
               return err
           }); err != nil {
               return nil, err
           }
           if hit == nil {
               // Hit was cascade-deleted (parent breakpoint deleted via
               // ON DELETE CASCADE on rimsky_breakpoint_hits.breakpoint_id).
               // Treat as auto-resume with no overlay.
               return nil, nil
           }
           if hit.ResumedAt != nil {
               return hit, nil
           }
           select {
           case <-ctx.Done():
               return nil, ctx.Err()
           case <-time.After(breakpointResumePollInterval):
           }
       }
   }

   // buildSnapshot constructs the JSONB snapshot payload per spec §4.6.
   func buildSnapshot(cc CheckpointContext) map[string]any {
       snap := map[string]any{
           "checkpoint": cc.Checkpoint,
           "dispatch_context": map[string]any{
               "executor":          cc.Executor,
               "node_type":         cc.NodeType,
               "graph":             cc.Graph,
               "child_key":         cc.ChildKey,
               "merged_attributes": cc.MergedAttributes,
           },
           "node_run":      cc.NodeRunSnapshot,
           "held_claims":   cc.HeldClaims,
           "open_wait_set": cc.OpenWaitSet,
       }
       if cc.TerminalSignal != nil {
           snap["terminal_signal"] = map[string]any{
               "type":    string(cc.TerminalSignal.Type),
               "payload": cc.TerminalSignal.Payload,
           }
       }
       return snap
   }
   ```

2. `shared.DeepMergeJSON` (from `foundation/shared/jsonmerge.go`) is the merge helper — package `shared`, not a `jsonmerge` sub-package. `args.Persist` follows the existing pattern in `runtime/runner_dispatch.go`; `args.Logger` follows the existing pattern.

3. Write `runtime/breakpoint_eval_test.go` covering:
   - Matcher mismatch → no hit row written.
   - signal_type prefix match / mismatch on after_terminal.
   - Pause mode: writes hit row, blocks until resume, applies overlay if present.
   - Notify-only mode: writes hit row, doesn't block.
   - Queue cap with drop_oldest: oldest hit deleted, dropped_count incremented.
   - Queue cap with block_dispatch: returns when one of the unresumed hits is resumed.
   - Hit cascade-deleted during wait: returns empty hit row (treated as auto-resume).

**Verification:** `go test ./runtime/... -count=1 -race -run TestBreakpoint`.

### Task 24: Wire `before_dispatch` checkpoint into `runtime/runner_dispatch.go`

**Files:**
- `runtime/runner_dispatch.go` (modify)
- `runtime/breakpoint_snapshot.go` (new — helpers building snapshot bits from the acquisition struct)

**Steps:**
1. Read `runtime/runner_dispatch.go` around the existing `applyAttributeOverrides` call site (around line 448). The current sequence is roughly:
   - line 449: `applyAttributeOverrides(...)` → produces `merged` + `matched`
   - line 460: `incrementMatchCountersAfterMerge(...)` (own tx, separate from acquisition tx)
   - line 462: first validation pass: `attributes.Validate(dispatchSchema, resolved, attributes.PhaseDispatch)`
   - lines 474-491: defense-in-depth re-validation against executor's raw schema

2. **Insert the breakpoint checkpoint between line 460 (counter increment) and line 462 (first validation).** Both subsequent validation passes (the dispatch-schema check and the executor-raw-schema defense-in-depth) then see the possibly-L6-mutated bag — so an invalid overlay surfaces via the existing `template_validation_failed` route per `concept:error-policy`. Pseudocode:

   ```go
   incrementMatchCountersAfterMerge(ctx, args.Persist, args.Logger, acq.InstanceID, matched)

   // Breakpoint checkpoint: before_dispatch. Runs outside any acquisition tx
   // (incrementMatchCountersAfterMerge committed its own short tx above; the
   // acquisition tx committed earlier per concept:supervisor invariants).
   // EvaluateBreakpoints opens its own short txns; pause-mode hits block
   // on waitForResume which polls on short txns. May return a different
   // `resolved` map if an L6 overlay was supplied at resume time.
   resolved, err = EvaluateBreakpoints(ctx, args, CheckpointContext{
       InstanceID:       acq.InstanceID,
       DispatchID:       acq.DispatchID,        // rimsky_node_runs.id
       FrameID:          acq.FrameID,
       Executor:         acq.Executor,
       NodeType:         acq.NodeType,
       Graph:            acq.GraphName,
       ChildKey:         scope.PartitionKey,
       MergedAttributes: resolved,
       Checkpoint:       "before_dispatch",
       TerminalSignal:   nil,
       NodeRunSnapshot:  nodeRunSnapshotForBreakpoint(acq),
       HeldClaims:       heldClaimsSummaryForBreakpoint(acq),
       OpenWaitSet:      openWaitSetSummaryForBreakpoint(ctx, args, acq),
   })
   if err != nil { return nil, schema, err }
   acq.MergedAttributes = resolved

   dispatchSchema := relaxRequiredToSourceDriven(schema)
   if err := attributes.Validate(dispatchSchema, resolved, attributes.PhaseDispatch); err != nil {
       return nil, schema, &attributeValidationError{Reason: "dispatch_bag_invalid", Cause: err}
   }
   // ... existing defense-in-depth re-validation at lines 474-491 unchanged.
   ```

3. The `acq` struct's identifier for the dispatch row is **`DispatchID`** (per `runtime/runner_acquire.go:99`), not `NodeRunID`. The persistence row's column name is `rimsky_node_runs.id`, but the Go field name throughout the runtime is `DispatchID`. The `CheckpointContext.DispatchID` field maps to `rimsky_breakpoint_hits.node_run_id` on the wire (the column name reflects the persistence model).

4. Create `runtime/breakpoint_snapshot.go` with the three helper functions. The acquisition struct is lowercase / unexported (`type acquisition struct {...}` per `runtime/runner_acquire.go:98`); helpers in the same `runtime` package take `*acquisition`:
   - `nodeRunSnapshotForBreakpoint(acq *acquisition) map[string]any` — projects the acquisition's node-run-row fields to a JSON-serializable map.
   - `heldClaimsSummaryForBreakpoint(acq *acquisition) []map[string]any` — summary: ID, alias, scope-summary string. Per `concept:inertness`, do NOT include scope/address/payload bytes.
   - `openWaitSetSummaryForBreakpoint(ctx context.Context, args RunArgs, acq *acquisition) []map[string]any` — queries the wait-set table for undrained rows where the receiver is `acq.DispatchID`. Opens its own short tx. Returns a list of `{sender_node_id, topic, name}` maps.

**Verification:** `go test ./runtime/... -count=1 -race`.

### Task 25: Wire `after_terminal` checkpoint at the two callers of `runApplyTerminal`

**Files:**
- `runtime/runner.go` (modify — line 482, the `runApplyTerminal` call site in the regular dispatch path)
- `runtime/callback.go` (modify — line 594, the `runApplyTerminal` call site in the async-callback path)
- `runtime/signal_for_terminal.go` (new — the `signalForTerminal` helper)

**Background:** `applyTerminal` at `runtime/runner_terminal.go:77` receives a `tx persistence.Tx` parameter and runs **inside an active transaction**. It is called only by `runApplyTerminal` at `runtime/runner_terminal.go:144`, which owns the surrounding tx. Calling `EvaluateBreakpoints` from inside `applyTerminal` would conflict with this tx (a pause-mode breakpoint would hold the tx across the indefinite wait). The correct insertion point is **after `runApplyTerminal` returns and its tx commits**, at each of its callers.

Concretely, `runApplyTerminal` has two callers:
- `runtime/runner.go:482` — the regular dispatch path (synchronous terminal arrival).
- `runtime/callback.go:594` — the async-callback path (terminal delivered via callback).

The breakpoint after_terminal checkpoint sits at each call site, immediately after the call returns successfully, before any downstream cascade or auto-terminal work runs.

**Steps:**
1. Create `runtime/signal_for_terminal.go` with a `signalForTerminal` helper that builds a `signal.Signal` from an executor terminal event. The construction logic for each terminal kind lives inline in each per-handler (`applyTerminalComplete`, `applyTerminalError`, `applyTerminalPark`, `applyTerminalInfraError`) at `runtime/runner_terminal.go`, `runtime/runner_terminal_handlers.go`, `runtime/runner_terminal_park.go`, `runtime/runner_error_policy.go` — read those to understand the kind→type-path mapping. The helper switches on the terminal's Kind and returns:
   ```go
   package runtime

   import (
       signalpkg "github.com/fallguyconsulting/rimsky/foundation/signal"
   )

   // signalForTerminal returns the signal.Signal envelope that
   // describes the just-applied terminal. Used by after_terminal
   // breakpoint evaluation to populate CheckpointContext.TerminalSignal.
   // The envelope shape matches what foundation/signal/audit/audit.go::EmitSignal
   // would persist for the same terminal — but no event row is written
   // here; that's audit-emit's job during applyTerminal.
   func signalForTerminal(t terminalEvent) signalpkg.Signal {
       switch t.Kind {
       case terminalComplete:
           return signalpkg.Signal{Type: signalpkg.TypePath("terminal/success"), Payload: ...}
       case terminalError:
           return signalpkg.Signal{Type: signalpkg.TypePath("terminal/error/" + t.ErrorClass), Payload: ...}
       case terminalPark:
           // Park kind has two leaves per concept:signal: snooze and await_callback.
           // Map from t.ParkReason.
           switch t.ParkReason {
           case "snooze":           return signalpkg.Signal{Type: "terminal/park/snooze", Payload: ...}
           case "await_callback":   return signalpkg.Signal{Type: "terminal/park/await_callback", Payload: ...}
           }
       case terminalInfraError:
           return signalpkg.Signal{Type: signalpkg.TypePath("terminal/infra/" + t.Reason), Payload: ...}
       }
       return signalpkg.Signal{}
   }
   ```
   The exact type names (`terminalEvent`, `terminalComplete`, etc.) and field names come from the existing runtime code — read it to use the right identifiers. The payload shapes per kind come from `foundation/signal/payloads.go`'s per-type struct definitions.

2. At `runtime/runner.go:482` (after `runApplyTerminal(...)` returns), insert:
   ```go
   sig := signalForTerminal(t)
   _, err = EvaluateBreakpoints(ctx, args, CheckpointContext{
       InstanceID:       acq.InstanceID,
       DispatchID:       acq.DispatchID,
       FrameID:          acq.FrameID,
       Executor:         acq.Executor,
       NodeType:         acq.NodeType,
       Graph:            acq.GraphName,
       ChildKey:         scope.PartitionKey,
       MergedAttributes: acq.MergedAttributes,
       Checkpoint:       "after_terminal",
       TerminalSignal:   &sig,
       NodeRunSnapshot:  nodeRunSnapshotForBreakpoint(acq),
       HeldClaims:       heldClaimsSummaryForBreakpoint(acq),
       OpenWaitSet:      openWaitSetSummaryForBreakpoint(ctx, args, acq),
   })
   if err != nil {
       args.Logger.Warn("breakpoint: after_terminal eval failed; continuing", "error", err.Error())
   }
   ```
   The local variable names (`acq`, `t`, `scope`) match the actual surrounding code — read line 482 to confirm.

3. Mirror at `runtime/callback.go:594` — same shape, possibly different local variable names (read the surrounding context).

4. The after_terminal checkpoint discards the return value of `EvaluateBreakpoints` — after-terminal overlays don't mutate further dispatch because the dispatch is already complete. Pause-mode breakpoints at after_terminal block the cascade until resume; that's the value. Notify-only breakpoints just observe. Failures in the breakpoint path are logged at Warn and swallowed (debugger problems shouldn't fail the run).

**Verification:** `go test ./runtime/... -count=1 -race`.

### Task 26: Verify transaction discipline and concurrent-frame safety

**Files:** `runtime/breakpoint_eval.go`, `runtime/runner_dispatch.go`, the after_terminal caller from Task 25 (read-only verification pass).

**Steps:**
1. The tx discipline is settled: every persistence call inside `EvaluateBreakpoints` opens its own short tx via `args.Persist.Transaction(...)`. There is no nil-tx path (which would panic per `foundation/persistence/postgres/backend.go::q`). Verify by grepping the file for any direct table-accessor call NOT wrapped in `Transaction`:
   ```
   grep -n "BreakpointHits\|Breakpoints\|WaitSet" runtime/breakpoint_eval.go runtime/breakpoint_snapshot.go
   ```
   Every match should be inside a `Transaction(ctx, func(ctx, tx) {...})` closure.
2. Verify the `before_dispatch` call site (Task 24) runs after `incrementMatchCountersAfterMerge` and BEFORE any further validation pass — so the L6 overlay is subject to both the dispatch-schema check AND the executor-raw-schema defense-in-depth check.
3. Verify the `after_terminal` call site (Task 25) runs after `applyTerminal` returns (tx committed) and before the cascade walks fire.
4. Concurrent-frame safety: pause-mode breakpoints block on a single dispatch (single `rimsky_node_runs.id`). Other frames running against the same instance are unaffected — they don't share the dispatch row. The waitForResume polling is per-hit; no global locks. Add a scenario test (Pass 8 Task 39) that verifies this.

**Verification:** `go test ./runtime/... -count=3 -race`.

---

## Pass 6: MCP server `resources/list` + `resources/read` extension

**Goal:** Extend the in-process MCP server at `control/controlapi/mcp/server.go::Server.ServeHTTP` to dispatch `resources/list` and `resources/read`. Advertise the `resources` capability in `initialize`. No subscribe, no notifications.

**Scope:** Tasks 27–29
**End state:** working
**Verification:** `go test ./control/controlapi/mcp/... -count=1 && make lint`

### Task 27: Extend the MCP dispatch switch with `resources/list` and `resources/read`

**Files:**
- `control/controlapi/mcp/server.go` (modify — extend `Server` struct, dispatch switch, handlers)
- `control/controlapi/mcp_route.go` (modify — extend `registerMCPRoute` at line 75 to construct the extended Server)

**Steps:**
1. Extend the `Server` struct at `control/controlapi/mcp/server.go:18` to carry the persistence accessor and the breakpoint-resources policy gate. The current shape is `type Server struct { Tools ToolCatalog }`. Add a `Resources` field of a new `ResourceCatalog` interface defined in the same file, paralleling `ToolCatalog`:
   ```go
   type Server struct {
       Tools     ToolCatalog
       Resources ResourceCatalog
   }

   // ResourceCatalog is the dependency Server uses to render
   // resources/list and resources/read responses. It mirrors
   // ToolCatalog's identity-and-permission-aware shape.
   type ResourceCatalog interface {
       // List returns the resources the requesting identity is allowed
       // to see, based on the identity attached to r.Context() by the
       // auth middleware.
       List(r *http.Request) ([]Resource, error)

       // Read fetches the contents of one resource by URI, gated by
       // permission (the implementation gates against breakpoint:read
       // for breakpoint-hits URIs). Returns the response body shape
       // per spec §6.4.
       Read(r *http.Request, uri string) (*ResourceContents, error)
   }

   // Resource is the resources/list entry shape.
   type Resource struct {
       URI         string `json:"uri"`
       Name        string `json:"name"`
       MimeType    string `json:"mimeType"`
       Description string `json:"description,omitempty"`
   }

   // ResourceContents is the resources/read response shape.
   type ResourceContents struct {
       URI      string `json:"uri"`
       MimeType string `json:"mimeType"`
       Text     string `json:"text"` // JSON-encoded body per spec §6.4
   }
   ```

2. In `Server.ServeHTTP`'s method-switch (currently `initialize`, `tools/list`, `tools/call`), add:
   ```go
   case "resources/list":
       s.handleResourcesList(w, r, req)
   case "resources/read":
       s.handleResourcesRead(w, r, req)
   ```

3. In `control/controlapi/mcp_route.go::registerMCPRoute` at line 75, the current wiring is `server := &mcp.Server{Tools: catalog}`. Extend to:
   ```go
   server := &mcp.Server{
       Tools:     catalog,
       Resources: newBreakpointResourceCatalog(deps),  // new helper
   }
   ```
   Define `newBreakpointResourceCatalog(deps AppDeps) mcp.ResourceCatalog` in `control/controlapi/mcp_route.go` (or a new `control/controlapi/mcp_resources.go`) — it returns a struct implementing `List` and `Read` via direct persistence reads:
   ```go
   type breakpointResourceCatalog struct {
       deps AppDeps
   }
   func newBreakpointResourceCatalog(deps AppDeps) mcp.ResourceCatalog {
       return &breakpointResourceCatalog{deps: deps}
   }
   func (c *breakpointResourceCatalog) List(r *http.Request) ([]mcp.Resource, error) {
       // Read the identity from r.Context(); enumerate instances the
       // requesting key has breakpoint:read for; build one Resource per
       // accessible instance with URI "rimsky://instances/{uuid}/breakpoint-hits".
       ...
   }
   func (c *breakpointResourceCatalog) Read(r *http.Request, uri string) (*mcp.ResourceContents, error) {
       // Parse the URI: rimsky://instances/{uuid}/breakpoint-hits?since=<seq>&limit=<n>
       // or rimsky://breakpoints/{uuid}/hits?since=<seq>&limit=<n>.
       // Gate against breakpoint:read for the resolved instance.
       // Call c.deps.Persist.BreakpointHits().ListSinceForInstance(...) or
       // ListSinceForBreakpoint(...) accordingly.
       // Marshal the response per spec §6.4 (hits + next_since + truncated).
       ...
   }
   ```
2. Update `handleInitialize` to advertise the resources capability:
   ```go
   func (s *Server) handleInitialize(w http.ResponseWriter, req Request) {
       writeRPCResult(w, req.ID, map[string]any{
           "protocolVersion": "2025-06-18",
           "capabilities": map[string]any{
               "tools":     map[string]any{},
               "resources": map[string]any{"subscribe": false, "listChanged": false},
           },
           "serverInfo": map[string]any{
               "name":    "rimsky-control-api",
               "version": "v1",
           },
       })
   }
   ```
3. Implement `handleResourcesList`:
   - Iterate the instances the requesting key has `breakpoint:read` permission for. The MCP server gets identity via the existing identity-hook pattern in `catalog.go`; reuse it.
   - For each accessible instance, emit a resource entry:
     ```jsonc
     {
       "uri":         "rimsky://instances/<uuid>/breakpoint-hits",
       "name":        "Breakpoint hits for instance <uuid>",
       "mimeType":    "application/x-rimsky-breakpoint-hits+json",
       "description": "Breakpoint hits for instance <uuid>. Read with ?since=<seq> and ?limit=<n>."
     }
     ```
   - Return `{"resources": [...]}`.
4. Implement `handleResourcesRead`:
   - Parse the `uri` parameter from the request `params`.
   - Match against the URI scheme `rimsky://instances/{instance_id}/breakpoint-hits` or `rimsky://breakpoints/{breakpoint_id}/hits`. Reject other URIs with `CodeInvalidParams`.
   - Parse query parameters `?since=<seq>` (default 0) and `?limit=<n>` (default 100, cap at 500).
   - Re-enter the chi router via the existing identity-hook pattern (this preserves auth and audit). The MCP catalog's `Invoke` mechanism handles this for tools; resources need a similar dispatch — write a small helper that performs the persistence query directly with the identity from the request context, since there's no HTTP route to delegate to.
   - Call `BreakpointHitTable.ListSinceForInstance` or `ListSinceForBreakpoint`. Marshal each row into the snapshot shape per spec §4.6.
   - Return:
     ```jsonc
     {
       "contents": [{
         "uri": "<original-uri>",
         "mimeType": "application/x-rimsky-breakpoint-hits+json",
         "text": "<json-marshal of {hits, next_since, truncated}>"
       }]
     }
     ```

**Verification:** `go test ./control/controlapi/mcp/... -count=1`.

### Task 28: Auto-expose new tool catalog entries for the debugger verbs

**Files:** verify, `control/controlapi/mcp_route.go::builtinSchemas` (read-only check)

**Steps:**
1. The MCP tool catalog is computed from `v1Actions` per the existing pattern. Confirm by reading `control/controlapi/mcp_route.go::builtinSchemas` and `control/controlapi/mcp/catalog.go`.
2. The 6 new action entries from Task 16 should automatically produce 6 new MCP tools: `instance_pause`, `instance_resume`, `breakpoint_read`, `breakpoint_create`, `breakpoint_resume`, `breakpoint_delete`. Confirm via a tools/list test.
3. If the catalog needs explicit per-tool schemas, add them in `builtinSchemas` per the existing pattern. The schemas are JSON-Schema-shaped objects with `properties`, `required`, etc.

**Verification:** `go test ./control/controlapi/mcp/... -count=1 -run TestToolsList`.

### Task 29: Write MCP scenario tests for `resources/list` and `resources/read`

**Files:** `control/controlapi/mcp/server_test.go` (extend) or a new `control/controlapi/mcp/resources_test.go`

**Steps:**
1. Add tests covering:
   - `resources/list` returns the instance-scoped URIs the requesting key has permission for.
   - A key without `breakpoint:read` for an instance does NOT see that instance's URI.
   - `resources/read` on a valid URI returns hits paginated by `?since` and `?limit`.
   - `resources/read` with `?since=<seq>` after that seq returns only newer hits.
   - Polling pattern: read, advance cursor by `next_since`, read again, get next page. Cover the `truncated=true → next page` flow.

**Verification:** `go test ./control/controlapi/mcp/... -count=1`.

---

## Pass 7: Reaper integration

**Goal:** Wire `BreakpointTable.SweepExpired` and `BreakpointHitTable.AutoResumeStale` into the existing reaper sweep cadence on the scheduler.

**Scope:** Tasks 30–31
**End state:** working
**Verification:** `go test ./cmd/... ./runtime/... -count=1 && make lint`

### Task 30: Add reaper hooks to the scheduler tick

**Files:** `graph/scheduler/scheduler.go` (modify)

**Steps:**
1. Read `graph/scheduler/scheduler.go` to understand the full sweep tick. The tick body contains multiple sweeps; the existing ones include `SweepOrphanedNodeRuns`, `SweepOrphanedClaimHandles`, `SweepClaimHandleRetention`, `SweepMessageIdempotencies`, `SweepReady`, `SweepParkedNodes`, `SweepOrphanedBlobs`, and `SweepDeliverMessagesForRunningFrames`. Some are gated behind `if cfg.X != nil` guards.

2. Locate the tick function's body (search via `grep -n "func.*Tick\|ScheduleSweeps\|RunTick" graph/scheduler/scheduler.go`). Find the closing `return nil` of the tick function. Add the breakpoint sweeps immediately before the `return nil`:

   ```go
   // Breakpoint sweeps per spec 2026-05-24-instance-debugger-design §7.4.
   // Errors are logged and swallowed — sweep failures don't crash the tick.
   // Note: the local variable names used here (persist, log) match the
   // scheduler context; the implementer adapts them to the actual identifiers
   // in scope at the insertion point.
   bpNow := cfg.Clock.Now()  // fresh `now` — local; the `now` in earlier blocks may be out of scope
   if err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
       deleted, err := persist.Breakpoints().SweepExpired(ctx, bpNow, tx)
       if err != nil { return err }
       if deleted > 0 {
           log.Info("tick: SweepExpired breakpoints", "deleted", deleted)
       }
       resumed, err := persist.BreakpointHits().AutoResumeStale(ctx, bpNow, tx)
       if err != nil { return err }
       if resumed > 0 {
           log.Info("tick: AutoResumeStale breakpoint hits", "resumed", resumed)
       }
       return nil
   }); err != nil {
       log.Warn("tick: breakpoint sweeps failed", "error", err.Error())
   }
   ```

3. The insertion point is "before the tick body's closing `return nil`" — this puts the breakpoint sweeps after all existing sweeps. The exact local-variable names (`persist`, `log`, `cfg.Clock`) reflect the scheduler.go context; the implementer adapts them to the actual identifiers in scope.

4. Errors are logged and swallowed, matching the existing `SweepClaimHandleRetention` discipline (`log.Warn(...)` on failure).

**Verification:** `go build ./graph/scheduler/... && go test ./graph/scheduler/... -count=1`.

### Task 31: Test the sweeper integration

**Files:** `graph/scheduler/scheduler_test.go` (extend) or, if scheduler has no good unit-test harness today, `foundation/persistence/postgres/breakpoint_hits_test.go` (extension)

**Steps:**
1. Find the existing scheduler tick test pattern via `grep -n "Sweep" graph/scheduler/*_test.go`. If a test invokes the tick loop with testcontainers Postgres and asserts on sweep effects, extend it.
2. The test should: bring up a Postgres DB, insert an expired breakpoint (`expires_at < now`) and a stale unresumed hit (`hit_at < now - hit_ttl_seconds` on an `auto_resume_after_ttl` breakpoint), run one tick, then assert:
   - Expired breakpoint row is deleted.
   - Stale hit row has `resumed_at IS NOT NULL` and `resumed_by_key = 'sweeper'`.
3. If extending scheduler-package tests is infeasible (no comparable existing test), put the test at the persistence layer: directly call `SweepExpired` and `AutoResumeStale` against a populated test DB and assert the same outcomes. This is a weaker integration but covers the SQL.

**Verification:** `go test ./graph/scheduler/... ./foundation/persistence/... -count=1 -race`.

---

## Pass 8: Scenario tests

**Goal:** End-to-end scenario coverage per spec §10.2.

**Scope:** Tasks 32–43
**End state:** working
**Verification:** `go test ./test/scenarios/breakpoints/... -count=1`

Each task creates one scenario test file under `test/scenarios/breakpoints/`. They follow the existing scenario-test conventions in `test/scenarios/`. The closest structural pattern to follow is the matcher-overlay e2e tests — specifically:
- **`test/scenarios/attribute_overrides_match_overlay_fanout_e2e_test.go`** — fan-out with matcher-overlay routing; shows how to build a template with multiple nodes, create an instance, dispatch through to terminal, and assert per-child outcomes.
- **`test/scenarios/attribute_overrides_match_overlay_subgraph_e2e_test.go`** — same with sub-graphs (relevant for the `graph` matcher key).
- **`test/scenarios/attribute_overrides_match_overlay_order_e2e_test.go`** — multi-entry overlay precedence (relevant for multi-breakpoint match).

Each scenario task below names the pattern file to base off; the implementer reads it for the harness setup (testcontainers Postgres, fake executor, frame plumbing, assertion helpers) and reuses the same shape.

### Task 32: `pause_resume_happy_path_test.go`

**Files:** `test/scenarios/breakpoints/pause_resume_happy_path_test.go` (new)

**Pattern to follow:** `test/scenarios/attribute_overrides_match_overlay_fanout_e2e_test.go` (single-instance e2e with template registration + dispatch + assertion). Reuse its harness scaffolding.

**Steps:**
1. Build the scenario per spec §10.2 "Pause-and-resume happy path": install pause-mode breakpoint via the new `POST /instances/{id}/breakpoints` route → start the dispatch → hit fires (assert hit row exists in `rimsky_breakpoint_hits`) → call resume RPC without overlay → confirm `resumed_at IS NOT NULL` → dispatch proceeds to terminal.
2. Use the fake-executor harness from the pattern file to control dispatch outcomes.

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestPauseResumeHappyPath`.

### Task 33: `resume_with_overlay_test.go`

**Files:** `test/scenarios/breakpoints/resume_with_overlay_test.go` (new)

**Steps:** Per spec §10.2 "Resume-with-overlay".

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestResumeWithOverlay`.

### Task 34: `resume_with_invalid_overlay_test.go`

**Files:** `test/scenarios/breakpoints/resume_with_invalid_overlay_test.go` (new)

**Steps:** Per spec §10.2 "Resume-with-invalid-overlay". Confirm 400 ErrResumeOverlayInvalid is returned and the hit stays paused.

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestResumeInvalidOverlay`.

### Task 35: `notify_only_mode_test.go`

**Files:** `test/scenarios/breakpoints/notify_only_mode_test.go` (new)

**Steps:** Per spec §10.2 "Notify-only mode".

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestNotifyOnlyMode`.

### Task 36: `multi_breakpoint_match_test.go`

**Files:** `test/scenarios/breakpoints/multi_breakpoint_match_test.go` (new)

**Steps:** Per spec §10.2 "Multi-breakpoint match".

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestMultiBreakpointMatch`.

### Task 37: `paused_on_create_then_install_test.go`

**Files:** `test/scenarios/breakpoints/paused_on_create_then_install_test.go` (new)

**Steps:** Per spec §10.2 "Paused-on-create + install + release".

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestPausedOnCreateThenInstall`.

### Task 38: `soft_instance_pause_test.go`

**Files:** `test/scenarios/breakpoints/soft_instance_pause_test.go` (new)

**Steps:** Per spec §10.2 "Soft instance pause".

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestSoftInstancePause`.

### Task 39: `concurrent_frame_correctness_test.go`

**Files:** `test/scenarios/breakpoints/concurrent_frame_correctness_test.go` (new)

**Steps:** Per spec §10.2 "Concurrent-frame correctness".

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestConcurrentFrameCorrectness`.

### Task 40: `hit_queue_overflow_drop_oldest_test.go`

**Files:** `test/scenarios/breakpoints/hit_queue_overflow_drop_oldest_test.go` (new)

**Steps:** Per spec §10.2 "Hit-queue overflow drop_oldest". 150 dispatches, 50 dropped, dropped_count = 50.

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestHitQueueOverflowDropOldest`.

### Task 41: `hit_auto_resume_ttl_test.go`

**Files:** `test/scenarios/breakpoints/hit_auto_resume_ttl_test.go` (new)

**Steps:** Per spec §10.2 "Hit auto-resume via TTL". Uses `hit_ttl_seconds = 1` for a fast test.

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestHitAutoResumeTTL`.

### Task 42: `signal_type_filter_test.go` and `breakpoint_expiry_test.go`

**Files:**
- `test/scenarios/breakpoints/signal_type_filter_test.go` (new)
- `test/scenarios/breakpoints/breakpoint_expiry_test.go` (new)

**Steps:** Per spec §10.2 "Signal-type filter on after_terminal" and "Breakpoint expiry".

**Verification:** `go test ./test/scenarios/breakpoints/... -count=1 -run TestSignalTypeFilter -run TestBreakpointExpiry`.

### Task 43: `orphan_hit_on_breakpoint_deletion_test.go` and persistence-consolidation tests

**Files:**
- `test/scenarios/breakpoints/orphan_hit_on_breakpoint_deletion_test.go` (new)
- `foundation/persistence/postgres/schema_consolidation_test.go` (new)
- `foundation/persistence/sqlite/schema_consolidation_test.go` (new)

**Steps:**
1. The orphan-hit test (per spec §10.2) verifies that deleting a breakpoint with a paused hit unblocks the dispatch (the hit is cascade-deleted, the waitForResume loop sees the row gone, treats it as auto-resume).
2. The consolidation tests verify:
   - Fresh-DB migration: testcontainers Postgres → run `Migrate` → schema matches the expected column / index / constraint set (introspect via `pg_catalog`).
   - Stale-`rimsky_migrations` migration: testcontainers Postgres → pre-seed `rimsky_migrations` with rows for `001-baseline.sql` through `014-drop-last-outcome.sql` on an otherwise-empty schema → run `Migrate` → `001-schema.sql` applies cleanly (no filename collision, the orphan rows are inert).

**Verification:** `go test ./test/scenarios/breakpoints/... ./foundation/persistence/... -count=1 -race`.

---

## Pass 9: Concept-doc mutations

**Goal:** Apply the 10 concept-doc mutations from spec §12 in a single pass.

**Scope:** Tasks 44–53
**End state:** working
**Verification:** `make lint` and a grep check that the new concept file exists and `concepts.md` regeneration matches.

### Task 44: Create `.ok-planner/design/concepts/breakpoint.md`

**Files:** `.ok-planner/design/concepts/breakpoint.md` (new)

**Steps:**
1. Read the template at `.ok-planner/design/concepts/_retired/` (any retired concept) or any current `concepts/*.md` for the front-matter shape (`---\nconcept: <slug>\nstatus: as-is\naliases: []\nreferences:\n  - <path>\n---`).
2. Create the file with the content per spec §12.1:

   ```markdown
   ---
   concept: breakpoint
   status: as-is
   aliases: []
   references:
     - ../../specs/2026-05-24-instance-debugger-design.md
   ---

   # Breakpoint

   ## What it is

   A breakpoint is a runtime-installed pause-point on a live `concept:instance`, identified by UUID and bound to a `(matcher, checkpoint, signal_type?, mode, overflow_policy, ttl_seconds?)` tuple. Persisted in `table:rimsky_instance_breakpoints`; hits in `table:rimsky_breakpoint_hits`.

   - `matcher` — closed five-key predicate from `concept:attribute`'s by_match shape (shared via `code:foundation/matcher/`); see `concept:attribute` §Matcher grammar.
   - `checkpoint` — `before_dispatch` or `after_terminal`; identifies where in the supervisor's per-dispatch flow the breakpoint fires.
   - `signal_type` — optional prefix-match against `concept:signal` type-paths (valid only for `after_terminal`); the operator's way to express "break only on terminal/error/*" etc.
   - `mode` — `pause` (block the runner until resume) or `notify_only` (record the hit and continue).
   - `overflow_policy` — `drop_oldest` (notify_only-only; default), `block_dispatch` (pause-only; default), or `auto_resume_after_ttl` (per-hit timeout).
   - `ttl_seconds` — optional auto-deletion of the breakpoint itself; instance-lifetime when NULL.

   ## Purpose

   Enable agent-driven debugging of live rimsky instances. The agent installs breakpoints at the dispatch points it cares about, optionally pauses execution, inspects the snapshot, and optionally mutates the dispatch via a one-shot overlay before resuming. This is the runtime-cooperative half of `concept:control-api`'s debugger surface; `concept:instance`'s paused/resume affordance is the other half (instance-level hold).

   ## Boundaries

   Owns: `table:rimsky_instance_breakpoints` and `table:rimsky_breakpoint_hits` (schema, CRUD, sweeps); the `before_dispatch` and `after_terminal` supervisor checkpoint logic in `code:runtime/breakpoint_eval.go`; the resume-with-overlay L6 merge; the per-mode overflow policies and the queue-cap (100 unresumed hits per breakpoint).

   Does NOT own: the matcher grammar itself (shared with `concept:attribute`'s by_match via `code:foundation/matcher/`); template-baked pauses (none exist — `concept:parked-state` is executor-emitted, this concept is operator-injected at runtime); the audit-log emission for the API surface (covered by the existing `auth.*` event kinds per `concept:event-log`); the MCP transport for hit delivery (`concept:control-api` owns the read-only `resources/list` / `resources/read` extension that surfaces hits).

   Adjacent: `concept:supervisor`, `concept:control-api`, `concept:attribute`, `concept:instance`, `concept:signal`, `concept:permission`, `concept:parked-state`.

   ## Invariants

   - Only the supervisor writes hit rows (`@blessed-invariant` candidate).
   - Resume is idempotent on `hit_id`: replays return the original outcome unchanged.
   - `signal_type` is rejected on `before_dispatch` breakpoints at registration.
   - `mode=pause + overflow_policy=drop_oldest` is rejected at registration (pause-mode hits cannot be silently dropped).
   - `mode=notify_only + overflow_policy=block_dispatch` is rejected at registration (the policy contradicts the mode's non-blocking semantics).
   - The L6 resume overlay applies only to the single dispatch that hit the breakpoint; it never persists into `col:rimsky_instances.attribute_overrides`.
   - Cascade-deletion of a breakpoint (via ON DELETE CASCADE on `rimsky_breakpoint_hits.breakpoint_id`) unblocks any paused runner waiting on a hit of that breakpoint, treating the missing-row case as auto-resume with no overlay.

   ## Aliases and historical names

   None.

   ## Open within this concept

   (none yet)

   ## Notes

   - 2026-05-24 — Introduced per spec `.ok-planner/specs/2026-05-24-instance-debugger-design.md`.
   ```

**Verification:** `ls .ok-planner/design/concepts/breakpoint.md` returns the new file.

### Task 45: Mutate `concepts/control-api.md`

**Files:** `.ok-planner/design/concepts/control-api.md` (modify)

**Steps:**
1. Per spec §12.2 first bullet:
   - In the "What it is" subsection's MCP description, update the parenthetical "Tools-only V1" to "Tools-only V1, plus read-only resources (`resources/list` and `resources/read`) added by spec 2026-05-24-instance-debugger-design. No `resources/subscribe` and no server-pushed notifications in V1 — those await a transport upgrade."
   - In the HTTP routes list, add: `POST /instances/{id}/pause`, `POST /instances/{id}/resume`, `POST /instances/{id}/breakpoints`, `GET /instances/{id}/breakpoints`, `DELETE /instances/{id}/breakpoints/{breakpoint_id}`, `POST /instances/{id}/breakpoints/{breakpoint_id}/resume`.
   - Append a Notes entry: `2026-05-24 — MCP capability extends from tools-only to tools + read-only resources per spec 2026-05-24-instance-debugger-design. resources/list and resources/read added to the dispatch switch; push (resources/subscribe + notifications/resources/updated) deferred to a future transport-upgrade spec. New /instances/{id}/pause and /resume routes added. New /instances/{id}/breakpoints/* routes added.`

**Verification:** `grep "2026-05-24" .ok-planner/design/concepts/control-api.md` returns the new Notes entry.

### Task 46: Mutate `concepts/instance.md`

**Files:** `.ok-planner/design/concepts/instance.md` (modify)

**Steps:**
1. Per spec §12.2 second bullet:
   - In the Boundaries section, add "paused state column" to the Owns list.
   - In the Invariants section, add: "Candidate selection by the supervisor skips paused instances (the candidate query filter includes `AND paused = false`)."
   - Append a Notes entry: `2026-05-24 — Adds rimsky_instances.paused BOOLEAN column and the corresponding pause / resume / paused-on-create surface per spec 2026-05-24-instance-debugger-design. Soft-pause semantics: in-flight dispatches run to terminal; new claims are held until resume.`

**Verification:** `grep "paused" .ok-planner/design/concepts/instance.md` returns the new additions.

### Task 47: Mutate `concepts/supervisor.md`

**Files:** `.ok-planner/design/concepts/supervisor.md` (modify)

**Steps:**
1. Per spec §12.2 third bullet:
   - In the Boundaries section, add: "breakpoint checkpoint evaluation at before_dispatch and after_terminal; blocked-runner polling for resume."
   - In the Invariants section, add: "Candidate selection skips paused instances and dispatches matching pause-mode breakpoints with unresumed hits."
   - Append a Notes entry: `2026-05-24 — Adds breakpoint checkpoint cooperation per spec 2026-05-24-instance-debugger-design. Pause-mode breakpoints block the runner until resume; notify_only breakpoints emit a hit row and continue. Pause-mode block uses polling (250ms) on rimsky_breakpoint_hits.resumed_at; no cross-process IPC bus.`

### Task 48: Mutate `concepts/signal.md`

**Files:** `.ok-planner/design/concepts/signal.md` (modify)

**Steps:**
1. Per spec §12.2 fourth bullet, append a Notes entry: `2026-05-24 — concept:breakpoint consumes signal type-paths via the signal_type filter on after_terminal breakpoints (prefix-only, trailing-* wildcards, validated via foundation/signal/taxonomy.go::ValidateTypePath). No taxonomy change; concept:signal is read-only consumer.`

### Task 49: Mutate `concepts/attribute.md`

**Files:** `.ok-planner/design/concepts/attribute.md` (modify)

**Steps:**
1. Per spec §12.2 fifth bullet, append a Notes entry: `2026-05-24 — Matcher grammar (the closed 5-key dispatch-identity predicate from by_match) extracts to foundation/matcher/ per spec 2026-05-24-instance-debugger-design. concept:breakpoint reuses the package. by_match wire shape, semantics, and merge layering unchanged.`

### Task 50: Mutate `concepts/persistence-database.md`

**Files:** `.ok-planner/design/concepts/persistence-database.md` (modify)

**Steps:**
1. Per spec §12.2 sixth bullet, append a Notes entry: `2026-05-24 — Migration history flattened per spec 2026-05-24-instance-debugger-design. The 14 numbered migrations (001-baseline through 014-drop-last-outcome) are deleted and replaced with a single consolidated 001-schema.sql per backend reflecting current schema state plus the new breakpoint tables and rimsky_instances.paused column. Pre-v1 break-freely operation; existing dev databases drop and recreate. Adds BreakpointTable and BreakpointHitTable accessors on Tables().`

### Task 51: Mutate `concepts/role-template.md`

**Files:** `.ok-planner/design/concepts/role-template.md` (modify)

**Steps:**
1. Per spec §12.2 seventh bullet:
   - Update the "V1 ships" enumeration to include `debug-operator`.
   - Append a Notes entry: `2026-05-24 — Adds debug-operator role-template per spec 2026-05-24-instance-debugger-design. Bundles *:read, instance:pause, instance:resume, breakpoint:create, breakpoint:resume, breakpoint:delete. High-risk in production; grant explicitly. agent-supervisor unchanged.`

### Task 52: Mutate `concepts/permission.md`

**Files:** `.ok-planner/design/concepts/permission.md` (modify)

**Steps:**
1. Per spec §12.2 eighth bullet, append a Notes entry: `2026-05-24 — Adds breakpoint:* and instance:pause / instance:resume action verbs to the canonical registry per spec 2026-05-24-instance-debugger-design. breakpoint:read covered by *:read wildcard; the four writes (create, resume, delete, instance:pause, instance:resume) require explicit grant via the new debug-operator role-template.`

### Task 53: Mutate `concepts/parked-state.md` and `concepts/inertness.md`; regenerate `concepts.md` TOC

**Files:**
- `.ok-planner/design/concepts/parked-state.md` (modify)
- `.ok-planner/design/concepts/inertness.md` (modify)
- `.ok-planner/design/concepts.md` (regenerate)

**Steps:**
1. `parked-state.md` — per spec §12.2 ninth bullet, append a Notes entry: `2026-05-24 — concept:breakpoint is the operator-injected sibling to executor-emitted parked-state. Breakpoint pause-mode blocks the runner at supervisor checkpoints; parked-state is the executor's own hold via Park terminal. The two are distinct primitives serving different control directions; per spec 2026-05-24-instance-debugger-design.`

2. `inertness.md` — per spec §12.2 tenth bullet:
   - Locate the "Sanctioned read sites" enumeration (cross-cutting boundary section).
   - Update the entry that cites `evaluateMatcher (code:runtime/attribute_overrides.go, ...)`. Replace with `Evaluate (code:foundation/matcher/matcher.go, attrs.<path> branch)`.
   - Append a Notes entry: `2026-05-24 — Matcher evaluator extracted to foundation/matcher/ per spec 2026-05-24-instance-debugger-design. The sanctioned attribute-value read site for matcher predicates is now code:foundation/matcher/matcher.go::Evaluate (attrs.<path> branch). by_match in runtime/attribute_overrides.go::applyAttributeOverrides delegates to the shared package; the inertness discipline is unchanged.`

3. `concepts.md` — per `.ok-planner/CLAUDE.md`, this file is auto-generated and refreshed by `execute-plan` when a plan touches `concepts/`. The new `concepts/breakpoint.md` file (Task 44) plus the mutations to existing concept docs (Tasks 45-53) will trigger the regeneration when `execute-plan` finishes this plan. **Do not hand-edit `concepts.md`** — let the generator produce it. If at end-of-plan the file lacks a `breakpoint` entry, that's a generator bug to surface, not something to paper over with a hand-edit.

**Verification:** All four file modifications applied (`parked-state.md`, `inertness.md`, `concepts/breakpoint.md` existence from Task 44). `make lint` (Go-level lint; design-doc files aren't lint-targeted). `concepts.md` regeneration happens at execute-plan finalization per `.ok-planner/CLAUDE.md`.

**Final pass verification:**
```
go build ./... \
  && go test ./... -count=1 \
  && go test ./test/scenarios/breakpoints/... ./foundation/persistence/... -count=1 -race \
  && make lint
```

---

## Manual checks after completion

None required for this plan. All verification is automated.
