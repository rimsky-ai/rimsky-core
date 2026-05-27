# Fan-out + Sub-graph Safety: RunScope-First Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`
**Goal:** Reshape `rimsky_node_runs` to use `run_scope_id` (FK to a new `rimsky_run_scopes` table) instead of inline `(parent_run_id, child_key)`; rename the existing `Scope` concept to `ClaimScope` to disambiguate; close the fan-out + sub-graph safety bug class at the data-model level; ship the consequent renames, fixes, scenarios, and concept-doc mutations as one coherent delivery.
**Architecture:** New first-class `RunScope` entity (`rimsky_run_scopes` table) hosts each graph instantiation (main / sub-graph / fan-out partition). Tree shape lives on the scope table via `parent_run_scope_id`. Run rows carry `run_scope_id` non-null FK. Allocation stays lazy via a narrow `AffirmNodeRunRow` primitive; eager allocation is a future no-op rewrite. Cascade walker carries scope through edges; callback path enforces deterministic phase check; recovery-aware fields exposed on the executor wire.
**Tech Stack:** Go 1.22+; PostgreSQL JSONB + SQLite TEXT; `jackc/pgx/v5`; `modernc.org/sqlite`; `protocols/proto/v1/*.proto`; existing test scenario harness at `code:graph/scenario/`; existing conformance suite at `code:foundation/persistence/conformance/`.

---

## Preflight assumptions

The spec assumes all prior cleanup-cycle work has landed. As of plan-writing, these are in tree (some uncommitted):

- Matcher overlay for `attribute_overrides` (L5 `by_match`) — landed.
- Fan-out recursion guard at `code:runtime/runner_acquire_helpers.go::acquireFanOutIfDeclared` (one-line `if out.ParentRunID != nil { return nil }`) — landed.
- Sub-graph entry absorption in `code:graph/node/template_validator_graphs.go::flatten` — landed.
- Per-run disambiguator threading on nine persistence methods (`Nodes.UpdateState`/`UpdateHeartbeat`/`ClearLastOutcome`/`ClearSupervisorAssignment`; `Queue.GetParkedByNode`/`RemoveForNodeInTx`/`EnqueueInTx`/`GetInFlightRunForNode`; `SetRetryNoProgressForNodeInTx`) — landed.

If preconditions are unmet, **stop** and surface to the user. This plan reshapes around the post-cleanup state.

Migration numbering: the next migration number is **007** (the last existing migration is `006-attribute-overrides-match-counts.sql` in both postgres and sqlite). All new migrations in this plan use sequential numbers starting at 007.

Naming reminder used throughout this plan:
- **RunScope** = execution context (new concept).
- **ClaimScope** = renamed from `Scope` (claim-identity bytes).
- **RunSheet** = prose-only noun for "all in-flight runs across all RunScopes" — never a database entity, never a Go type.

---

## Task 1 — Postgres migration 007: create `rimsky_run_scopes` table

**Files:** `foundation/persistence/postgres/migrations/007-run-scopes.sql` (new)

**Steps:**

1. Create the file with this content:

   ```sql
   -- =====  rimsky_run_scopes  =====
   -- First-class execution context per concept:run-scope. Hosts the set
   -- of rimsky_node_runs rows for one graph instantiation (main /
   -- subgraph / fanout_partition). Tree shape via parent_run_scope_id.
   -- Per spec
   -- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
   -- §"Schema / rimsky_run_scopes".
   CREATE TABLE rimsky_run_scopes (
       id                  UUID PRIMARY KEY,
       parent_run_scope_id UUID NULL REFERENCES rimsky_run_scopes(id),
       parent_run_id       UUID NULL REFERENCES rimsky_node_runs(id),
       graph_name          TEXT NOT NULL,
       partition_key       TEXT NOT NULL DEFAULT '',
       instance_id         UUID NOT NULL REFERENCES rimsky_instances(id),
       created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       closed_at           TIMESTAMPTZ NULL,

       CONSTRAINT run_scope_main_has_no_parents CHECK (
         (parent_run_scope_id IS NULL AND parent_run_id IS NULL)
         OR
         (parent_run_scope_id IS NOT NULL AND parent_run_id IS NOT NULL)
       )
   );

   -- At most one open fan-out partition RunScope per (parent_run_id, partition_key).
   CREATE UNIQUE INDEX uq_run_scopes_fanout_partition_open
       ON rimsky_run_scopes (parent_run_id, partition_key)
       WHERE partition_key != '' AND closed_at IS NULL;

   -- Tree-walk index: parent_chain navigation for depth-gating + aggregation.
   CREATE INDEX idx_run_scopes_parent_chain ON rimsky_run_scopes (parent_run_scope_id);
   ```

**Verification:**
```
test -f foundation/persistence/postgres/migrations/007-run-scopes.sql && \
  head -3 foundation/persistence/postgres/migrations/007-run-scopes.sql
```

---

## Task 2 — SQLite migration 007: parallel `rimsky_run_scopes` table

**Files:** `foundation/persistence/sqlite/migrations/007-run-scopes.sql` (new)

**Steps:**

1. Create the file with SQLite-flavored equivalents:

   ```sql
   -- =====  rimsky_run_scopes  =====
   -- SQLite parallel of postgres migration 007. Per spec
   -- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
   CREATE TABLE rimsky_run_scopes (
       id                  TEXT PRIMARY KEY,
       parent_run_scope_id TEXT NULL REFERENCES rimsky_run_scopes(id),
       parent_run_id       TEXT NULL REFERENCES rimsky_node_runs(id),
       graph_name          TEXT NOT NULL,
       partition_key       TEXT NOT NULL DEFAULT '',
       instance_id         TEXT NOT NULL REFERENCES rimsky_instances(id),
       created_at          TEXT NOT NULL DEFAULT (datetime('now')),
       closed_at           TEXT NULL,
       CHECK (
         (parent_run_scope_id IS NULL AND parent_run_id IS NULL)
         OR
         (parent_run_scope_id IS NOT NULL AND parent_run_id IS NOT NULL)
       )
   );

   CREATE UNIQUE INDEX uq_run_scopes_fanout_partition_open
       ON rimsky_run_scopes (parent_run_id, partition_key)
       WHERE partition_key != '' AND closed_at IS NULL;

   CREATE INDEX idx_run_scopes_parent_chain ON rimsky_run_scopes (parent_run_scope_id);
   ```

**Verification:**
```
test -f foundation/persistence/sqlite/migrations/007-run-scopes.sql
```

---

## Task 3 — Postgres migration 008: reshape `rimsky_node_runs`

**Files:** `foundation/persistence/postgres/migrations/008-node-runs-run-scope-id.sql` (new)

**Steps:**

1. Pre-v1 break-freely (per `code:submodules/rimsky/.claude/rules/rules.md`): drop the in-flight columns and indexes; add the new column and index. No data preservation.

   ```sql
   -- =====  rimsky_node_runs.run_scope_id  =====
   -- Replace inline (parent_run_id, child_key) with non-null FK to
   -- rimsky_run_scopes. Collapse the two partial-unique in-flight
   -- indexes to one keyed on (node_id, run_scope_id). Per spec
   -- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
   --
   -- The rimsky_node_runs_child_key_check CHECK constraint references
   -- BOTH parent_run_id and child_key (declared at migration 001 line
   -- ~178); drop it before the column drops. Postgres cascades index
   -- drops with the column drop, but the named CHECK does not, so
   -- explicit DROP CONSTRAINT first. (See migration 008-sqlite for
   -- the SQLite parallel that ALSO drops idx_node_runs_parent_run_id
   -- explicitly because SQLite doesn't cascade index drops.)
   ALTER TABLE rimsky_node_runs DROP CONSTRAINT rimsky_node_runs_child_key_check;

   DROP INDEX IF EXISTS uq_node_runs_in_flight_per_root_node;
   DROP INDEX IF EXISTS uq_node_runs_in_flight_per_child;

   ALTER TABLE rimsky_node_runs DROP COLUMN parent_run_id;
   ALTER TABLE rimsky_node_runs DROP COLUMN child_key;

   ALTER TABLE rimsky_node_runs
       ADD COLUMN run_scope_id UUID NOT NULL REFERENCES rimsky_run_scopes(id);

   CREATE UNIQUE INDEX uq_node_runs_in_flight_per_run_scope
       ON rimsky_node_runs (node_id, run_scope_id)
       WHERE phase IN ('pending', 'active', 'held', 'parked');
   ```

   Implementer: verify the actual CHECK constraint name in `foundation/persistence/postgres/migrations/001-baseline.sql` (around line 178); the constraint might be named differently or might be column-inline (in which case it goes away with the column drop and the explicit DROP CONSTRAINT is unnecessary).

**Verification:**
```
test -f foundation/persistence/postgres/migrations/008-node-runs-run-scope-id.sql
```

---

## Task 4 — SQLite migration 008: parallel reshape

**Files:** `foundation/persistence/sqlite/migrations/008-node-runs-run-scope-id.sql` (new)

**Steps:**

1. SQLite does not support `DROP COLUMN` on older versions but does as of 3.35. Use ALTER directly; if a test environment uses an older SQLite, the implementer adjusts to recreate-table-via-`INSERT INTO ... SELECT` pattern (the existing migrations use straightforward ALTER, so this should work).

   ```sql
   -- =====  rimsky_node_runs.run_scope_id  =====
   -- SQLite parallel of postgres migration 008. Per spec.
   -- SQLite does not cascade index drops automatically — must DROP
   -- INDEX explicitly before dropping the columns the indexes reference.
   DROP INDEX IF EXISTS uq_node_runs_in_flight_per_root_node;
   DROP INDEX IF EXISTS uq_node_runs_in_flight_per_child;
   DROP INDEX IF EXISTS idx_node_runs_parent_run_id;

   ALTER TABLE rimsky_node_runs DROP COLUMN parent_run_id;
   ALTER TABLE rimsky_node_runs DROP COLUMN child_key;

   ALTER TABLE rimsky_node_runs
       ADD COLUMN run_scope_id TEXT NOT NULL REFERENCES rimsky_run_scopes(id);

   CREATE UNIQUE INDEX uq_node_runs_in_flight_per_run_scope
       ON rimsky_node_runs (node_id, run_scope_id)
       WHERE phase IN ('pending', 'active', 'held', 'parked');
   ```

**Verification:**
```
test -f foundation/persistence/sqlite/migrations/008-node-runs-run-scope-id.sql
```

---

## Task 5 — Postgres migration 009: rename ClaimScope schema artifacts

**Files:** `foundation/persistence/postgres/migrations/009-claim-scope-rename.sql` (new)

**Steps:**

1. Rename column, CHECK constraint value, and index per spec §"Rename 2: `scope` (claim-identity bytes) → `claim_scope`":

   ```sql
   -- =====  Scope → ClaimScope rename  =====
   -- Rename rimsky_claim_handles.scope_data → claim_scope_data.
   -- Update lock_kind CHECK constraint enum: 'scope' → 'claim_scope'.
   -- Drop+recreate rimsky_claim_handles_required_columns_check which
   -- also embeds the old 'scope' enum value AND the old scope_data
   -- column name.
   -- Rename index idx_rimsky_claim_handles_scope → ..._claim_scope.
   -- Per spec
   -- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
   -- §"Rename 2".
   ALTER TABLE rimsky_claim_handles RENAME COLUMN scope_data TO claim_scope_data;

   -- Update the data first so the new CHECK constraint passes.
   UPDATE rimsky_claim_handles SET lock_kind = 'claim_scope' WHERE lock_kind = 'scope';

   -- Drop and recreate the lock_kind enum CHECK.
   ALTER TABLE rimsky_claim_handles DROP CONSTRAINT rimsky_claim_handles_lock_kind_check;
   ALTER TABLE rimsky_claim_handles ADD CONSTRAINT rimsky_claim_handles_lock_kind_check
       CHECK (lock_kind IN ('named', 'claim_scope'));

   -- Drop and recreate the kind-fields CHECK which references both
   -- the renamed column and the renamed enum value. Per
   -- foundation/persistence/postgres/migrations/001-baseline.sql:349
   -- the actual constraint name is `claim_handle_kind_fields`.
   ALTER TABLE rimsky_claim_handles DROP CONSTRAINT claim_handle_kind_fields;
   ALTER TABLE rimsky_claim_handles ADD CONSTRAINT claim_handle_kind_fields
       CHECK (
         (lock_kind = 'claim_scope' AND producer_name IS NOT NULL AND claim_scope_data IS NOT NULL)
         OR
         (lock_kind = 'named' AND lock_name IS NOT NULL)
       );

   ALTER INDEX idx_rimsky_claim_handles_scope RENAME TO idx_rimsky_claim_handles_claim_scope;
   ```

   Notes:
   - `claim_handle_kind_fields` is the actual constraint name (verified at migration 001 line 349); it is a table-level NAMED CHECK. The `lock_kind`-inline CHECK at line 323 is column-inline and **unnamed at the SQL level**. Postgres auto-generates a name (typically `rimsky_claim_handles_lock_kind_check` for inline CHECKs on column `lock_kind`), but the name is implementation-defined and may differ.
   - **Fallback for the column-inline CHECK:** if `DROP CONSTRAINT rimsky_claim_handles_lock_kind_check` fails because the auto-name differs, the implementer can either (a) query `pg_constraint` for the actual name (`SELECT conname FROM pg_constraint WHERE conrelid = 'rimsky_claim_handles'::regclass AND consrc LIKE '%lock_kind%'` or similar), or (b) drop and recreate the column to remove the inline check. Approach (a) is preferred (cheaper); approach (b) is a fallback if the name can't be resolved.
   - The `claim_handle_kind_fields` body shown here is illustrative; read the existing CHECK in `foundation/persistence/postgres/migrations/001-baseline.sql` (around lines 350-351) and reproduce its shape with the renamed identifiers.

**Verification:**
```
test -f foundation/persistence/postgres/migrations/009-claim-scope-rename.sql
```

---

## Task 6 — SQLite migration 009: parallel ClaimScope rename

**Files:** `foundation/persistence/sqlite/migrations/009-claim-scope-rename.sql` (new)

**Steps:**

1. SQLite's CHECK constraints are tied to the table; renaming the constraint requires recreating the table. Pre-v1 break-freely lets us drop + recreate cleanly:

   ```sql
   -- =====  Scope → ClaimScope rename  =====
   -- SQLite parallel. Recreate rimsky_claim_handles with the renamed
   -- column, updated CHECK enum, and renamed index.
   -- Per spec.
   ALTER TABLE rimsky_claim_handles RENAME COLUMN scope_data TO claim_scope_data;
   UPDATE rimsky_claim_handles SET lock_kind = 'claim_scope' WHERE lock_kind = 'scope';
   DROP INDEX IF EXISTS idx_rimsky_claim_handles_scope;

   -- SQLite cannot alter CHECK constraints; recreate the table.
   -- Use a temp-rename + copy + drop pattern.
   ALTER TABLE rimsky_claim_handles RENAME TO rimsky_claim_handles_old;
   -- (re-run the migration-001 CREATE TABLE with the updated CHECK constraint:
   --   CHECK (lock_kind IN ('named', 'claim_scope'))
   --  copied verbatim from current 001-baseline.sql with the one-word swap)
   -- [Implementer: paste the rimsky_claim_handles CREATE TABLE statement
   --  from foundation/persistence/sqlite/migrations/001-baseline.sql here
   --  with the lock_kind CHECK updated and the column renamed.]
   INSERT INTO rimsky_claim_handles SELECT * FROM rimsky_claim_handles_old;
   DROP TABLE rimsky_claim_handles_old;

   CREATE INDEX idx_rimsky_claim_handles_claim_scope
       ON rimsky_claim_handles (producer_name) WHERE lock_kind = 'claim_scope';
   ```

   The implementer reads `foundation/persistence/sqlite/migrations/001-baseline.sql` to find the actual `CREATE TABLE rimsky_claim_handles (...)` statement and pastes it (with the column rename + CHECK update) into the placeholder marked `[Implementer: paste...]` above.

**Verification:**
```
test -f foundation/persistence/sqlite/migrations/009-claim-scope-rename.sql
```

---

## Task 7 — Define `RunScopeRow` + `RunScopeTable` interface

**Files:** `foundation/persistence/run_scopes.go` (new)

**Steps:**

1. Create the file with the interface and row type per spec §"`code:foundation/persistence.RunTreeTable` (reshape)" subsection on `RunScopeTable`:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   // run_scopes.go is the persistence accessor for rimsky_run_scopes,
   // the table backing concept:run-scope. Hosts the per-graph
   // instantiation tree (main / subgraph / fanout_partition).
   //
   // @concept: run-scope
   package persistence

   import (
       "context"
       "time"

       "github.com/rimsky-ai/rimsky-core/foundation/shared"
   )

   // RunScopeRow projects one rimsky_run_scopes row. ParentRunScopeID
   // and ParentRunID are nil only for the main RunScope; the table's
   // CHECK constraint enforces both-or-neither.
   type RunScopeRow struct {
       ID               shared.UUID
       ParentRunScopeID *shared.UUID
       ParentRunID      *shared.UUID
       GraphName        string
       PartitionKey     string  // non-empty iff fanout_partition kind
       InstanceID       shared.UUID
       CreatedAt        time.Time
       ClosedAt         *time.Time
   }

   // RunScopeTable is the persistence accessor for rimsky_run_scopes.
   // All methods take an explicit tx.
   //
   // @agent-contract:
   //   what:        RunScope CRUD on rimsky_run_scopes.
   //   how to use:  Create() inserts atomically with the triggering
   //                operation (instance create, subgraph caller success,
   //                SplitScope sub-claim acquisition). Close() stamps
   //                closed_at when parent-run rendezvous fires.
   //   handles:     scope tree shape, fanout_partition uniqueness,
   //                parent-chain walks.
   //   does NOT:    rimsky_node_runs allocation (see AffirmNodeRunRow);
   //                aggregation policy (see RunTreeRow.AggregationPolicy).
   //   threadsafe:  caller's tx isolation.
   type RunScopeTable interface {
       // Create inserts a new RunScope row. The caller supplies the id
       // so the same tx can also INSERT the first node_run row
       // referring to it (avoids a returning-id round-trip).
       Create(ctx context.Context, tx Tx, row RunScopeRow) error

       // GetByID returns the RunScope by id, or nil if not found.
       GetByID(ctx context.Context, tx Tx, id shared.UUID) (*RunScopeRow, error)

       // GetFanoutPartition returns the fanout_partition RunScope for
       // (parentRunID, partitionKey), or nil if not found. Used by
       // fan-out child re-resolution and by the cascade walker when
       // computing cross-scope targets.
       GetFanoutPartition(ctx context.Context, tx Tx, parentRunID shared.UUID, partitionKey string) (*RunScopeRow, error)

       // Close stamps closed_at on the RunScope. Called by carry-rule
       // (subgraph), aggregation walk (fanout_partition), and instance
       // termination (main). Idempotent: re-closing is a no-op.
       Close(ctx context.Context, tx Tx, id shared.UUID) error

       // ListChildScopes returns immediate child RunScopes for a parent
       // run. Used by aggregation walks and forensics.
       ListChildScopes(ctx context.Context, tx Tx, parentRunID shared.UUID) ([]RunScopeRow, error)

       // ListParentChain walks up via parent_run_scope_id; returns
       // from the given id (leaf) to the main RunScope (root)
       // inclusive. Used by depth-gating and forensics.
       ListParentChain(ctx context.Context, tx Tx, id shared.UUID) ([]RunScopeRow, error)
   }

   // ErrRunScopeClosed is returned by AffirmNodeRunRow when the
   // RunScope's closed_at is set. Sibling sentinel to ErrRunRowMissing.
   var ErrRunScopeClosed = errors.New("persistence: run scope is closed")
   ```

   (Add the missing `errors` import.)

**Verification:**
```
go build ./foundation/persistence/
```

---

## Task 8 — Register `RunScopeTable` on the `Tables` interface

**Files:** `foundation/persistence/tables.go` (modify; this is the file that defines the `Tables` interface — NOT `persistence.go`)

**Steps:**

1. Open `foundation/persistence/tables.go` (the actual location of the `Tables` interface; verified by grep). Locate the interface that exposes accessor methods like `Nodes()`, `Instances()`, `RunTree()`, etc.

2. Add a new method to the `Tables` interface:

   ```go
   // RunScopes returns the rimsky_run_scopes accessor.
   RunScopes() RunScopeTable
   ```

3. Note: `Queue` is **not** on `Tables` — it lives on `Database` (at `foundation/persistence/database.go`). The new `RunScopes` accessor follows the `Tables` pattern (table-scoped accessor inside a transaction), not the `Database` pattern.

4. Find the implementation struct (likely `tablesImpl` in `tables.go` itself, or in a sibling) and add the corresponding method returning a `RunScopeTable` impl. Defer the per-backend impl wiring to tasks 9 + 10.

**Verification:**
```
go build ./foundation/persistence/
```
The build will fail because the postgres + sqlite Store impls don't satisfy the new interface yet. That's expected and addressed in tasks 9 + 10.

---

## Task 9 — Postgres impl: `runScopesImpl`

**Files:** `foundation/persistence/postgres/run_scopes.go` (new); `foundation/persistence/postgres/backend.go` (modify to register the accessor)

**Steps:**

1. Create `foundation/persistence/postgres/run_scopes.go` with the impl. Use the existing `code:foundation/persistence/postgres/nodes.go` as the structural template:

   ```go
   package postgres

   import (
       "context"
       "errors"
       "fmt"

       "github.com/jackc/pgx/v5"

       persistence "github.com/rimsky-ai/rimsky-core/foundation/persistence"
       foundationshared "github.com/rimsky-ai/rimsky-core/foundation/shared"
   )

   const runScopeCols = `id, parent_run_scope_id, parent_run_id, graph_name, partition_key, instance_id, created_at, closed_at`

   type runScopesImpl struct {
       q func(persistence.Tx) querier
   }

   func newRunScopes(q func(persistence.Tx) querier) *runScopesImpl {
       return &runScopesImpl{q: q}
   }

   func (s *runScopesImpl) Create(ctx context.Context, tx persistence.Tx, row persistence.RunScopeRow) error {
       // CreatedAt is time.Time (non-pointer); if the caller leaves it
       // as the zero value, fall back to the DB default via NOW().
       // ClosedAt is *time.Time and may be nil (open scope).
       var createdAt any
       if row.CreatedAt.IsZero() {
           createdAt = nil  // will trigger COALESCE → NOW()
       } else {
           createdAt = row.CreatedAt
       }
       _, err := s.q(tx).Exec(ctx,
           `INSERT INTO rimsky_run_scopes (id, parent_run_scope_id, parent_run_id, graph_name, partition_key, instance_id, created_at, closed_at)
            VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, NOW()), $8)`,
           row.ID, row.ParentRunScopeID, row.ParentRunID, row.GraphName, row.PartitionKey, row.InstanceID, createdAt, row.ClosedAt)
       if err != nil {
           return fmt.Errorf("runScopes.Create: %w", err)
       }
       return nil
   }

   func (s *runScopesImpl) GetByID(ctx context.Context, tx persistence.Tx, id foundationshared.UUID) (*persistence.RunScopeRow, error) {
       row, err := scanRunScope(s.q(tx).QueryRow(ctx,
           `SELECT `+runScopeCols+` FROM rimsky_run_scopes WHERE id = $1`, id))
       if errors.Is(err, pgx.ErrNoRows) {
           return nil, nil
       }
       if err != nil {
           return nil, fmt.Errorf("runScopes.GetByID: %w", err)
       }
       return &row, nil
   }

   func (s *runScopesImpl) GetFanoutPartition(ctx context.Context, tx persistence.Tx, parentRunID foundationshared.UUID, partitionKey string) (*persistence.RunScopeRow, error) {
       row, err := scanRunScope(s.q(tx).QueryRow(ctx,
           `SELECT `+runScopeCols+` FROM rimsky_run_scopes
              WHERE parent_run_id = $1 AND partition_key = $2 AND closed_at IS NULL`,
           parentRunID, partitionKey))
       if errors.Is(err, pgx.ErrNoRows) {
           return nil, nil
       }
       if err != nil {
           return nil, fmt.Errorf("runScopes.GetFanoutPartition: %w", err)
       }
       return &row, nil
   }

   func (s *runScopesImpl) Close(ctx context.Context, tx persistence.Tx, id foundationshared.UUID) error {
       _, err := s.q(tx).Exec(ctx,
           `UPDATE rimsky_run_scopes SET closed_at = NOW() WHERE id = $1 AND closed_at IS NULL`, id)
       if err != nil {
           return fmt.Errorf("runScopes.Close: %w", err)
       }
       return nil
   }

   func (s *runScopesImpl) ListChildScopes(ctx context.Context, tx persistence.Tx, parentRunID foundationshared.UUID) ([]persistence.RunScopeRow, error) {
       rows, err := s.q(tx).Query(ctx,
           `SELECT `+runScopeCols+` FROM rimsky_run_scopes WHERE parent_run_id = $1 ORDER BY created_at`, parentRunID)
       if err != nil {
           return nil, fmt.Errorf("runScopes.ListChildScopes: %w", err)
       }
       defer rows.Close()
       var out []persistence.RunScopeRow
       for rows.Next() {
           r, err := scanRunScope(rows)
           if err != nil {
               return nil, fmt.Errorf("runScopes.ListChildScopes scan: %w", err)
           }
           out = append(out, r)
       }
       return out, nil
   }

   func (s *runScopesImpl) ListParentChain(ctx context.Context, tx persistence.Tx, id foundationshared.UUID) ([]persistence.RunScopeRow, error) {
       rows, err := s.q(tx).Query(ctx,
           `WITH RECURSIVE chain AS (
              SELECT `+runScopeCols+`, 0 AS depth FROM rimsky_run_scopes WHERE id = $1
              UNION ALL
              SELECT rs.id, rs.parent_run_scope_id, rs.parent_run_id, rs.graph_name, rs.partition_key, rs.instance_id, rs.created_at, rs.closed_at, chain.depth + 1
                FROM rimsky_run_scopes rs JOIN chain ON rs.id = chain.parent_run_scope_id
            )
            SELECT `+runScopeCols+` FROM chain ORDER BY depth`, id)
       if err != nil {
           return nil, fmt.Errorf("runScopes.ListParentChain: %w", err)
       }
       defer rows.Close()
       var out []persistence.RunScopeRow
       for rows.Next() {
           r, err := scanRunScope(rows)
           if err != nil {
               return nil, fmt.Errorf("runScopes.ListParentChain scan: %w", err)
           }
           out = append(out, r)
       }
       return out, nil
   }

   type runScopeScanner interface {
       Scan(dest ...any) error
   }

   func scanRunScope(s runScopeScanner) (persistence.RunScopeRow, error) {
       var r persistence.RunScopeRow
       err := s.Scan(&r.ID, &r.ParentRunScopeID, &r.ParentRunID, &r.GraphName, &r.PartitionKey, &r.InstanceID, &r.CreatedAt, &r.ClosedAt)
       return r, err
   }
   ```

2. Open `foundation/persistence/postgres/backend.go`. Find where other table accessors are registered on the `Store` struct (e.g., `s.nodes`, `s.queue`). Add `runScopes *runScopesImpl` to the struct, initialize in the constructor, and add the `RunScopes()` accessor returning `s.runScopes`.

**Verification:**
```
go build ./foundation/persistence/postgres/
```

---

## Task 10 — SQLite impl: `runScopesImpl`

**Files:** `foundation/persistence/sqlite/run_scopes.go` (new); `foundation/persistence/sqlite/backend.go` (modify)

**Steps:**

1. Mirror Task 9 in SQLite idiom. Key differences vs. postgres: use `?` placeholders, `datetime('now')` for `NOW()`, `database/sql` scanner pattern. Cribbing from `code:foundation/persistence/sqlite/nodes.go`'s shape.

2. Register on the SQLite `Store` struct's `RunScopes()` accessor.

**Verification:**
```
go build ./foundation/persistence/sqlite/
```

---

## Task 11 — Add `AffirmNodeRunRow` to `NodeTable` interface

**Files:** `foundation/persistence/nodes.go` (modify)

**Steps:**

1. Open `foundation/persistence/nodes.go`. Locate the `NodeTable` interface.

2. Add a new method declaration:

   ```go
   // AffirmNodeRunRow ensures an in-flight rimsky_node_runs row exists
   // for (nodeID, runScopeID). If no in-flight row exists, INSERTs a
   // pending stale row; if one exists, no-op. Returns only error.
   //
   // Callers MUST NOT depend on this method's return shape beyond
   // error/no-error. The architectural property: lazy↔eager
   // allocation is a no-op rewrite (every AffirmNodeRunRow call could
   // be deleted with no other code change if the system switched to
   // eager allocation at RunScope creation time).
   //
   // Errors:
   //   - ErrRunScopeClosed: the RunScope's closed_at is set.
   //   - underlying database errors: propagated.
   //
   // @blessed-invariant: AffirmNodeRunRow no-return-value-dependency
   // per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
   //
   // @concept: run-scope
   AffirmNodeRunRow(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx Tx) error
   ```

**Verification:**
```
go build ./foundation/persistence/
```
Build error from postgres + sqlite impls not satisfying the interface — expected; tasks 12 + 13 address.

---

## Task 12 — Postgres impl: `AffirmNodeRunRow`

**Files:** `foundation/persistence/postgres/nodes.go` (modify)

**Steps:**

1. Add the method to `nodesImpl`. The implementation first checks the RunScope is open, then INSERTs the run row if no in-flight row exists. Use a single INSERT...WHERE NOT EXISTS pattern that ALSO inner-joins to check `closed_at IS NULL`:

   ```go
   func (s *nodesImpl) AffirmNodeRunRow(ctx context.Context, nodeID foundationshared.UUID, runScopeID foundationshared.UUID, tx persistence.Tx) error {
       // First confirm the RunScope is open. A closed RunScope is an
       // implementation error in the caller; return ErrRunScopeClosed.
       var closedAt *string
       err := s.q(tx).QueryRow(ctx,
           `SELECT closed_at::text FROM rimsky_run_scopes WHERE id = $1`, runScopeID).Scan(&closedAt)
       if err != nil {
           return fmt.Errorf("AffirmNodeRunRow: lookup run_scope: %w", err)
       }
       if closedAt != nil {
           return persistence.ErrRunScopeClosed
       }
       // INSERT the run row only if no in-flight row exists. populates
       // required_stores from the template node-def via the same join
       // pattern as MarkStaleForCascade.
       _, err = s.q(tx).Exec(ctx,
           `INSERT INTO rimsky_node_runs
              (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
            SELECT gen_random_uuid(), n.id, n.executor,
                   COALESCE((
                     SELECT array_agg(store->>'name')
                       FROM rimsky_instances i
                       JOIN rimsky_templates t ON t.id = i.template_hash
                       CROSS JOIN LATERAL jsonb_array_elements(t.spec->'nodes') AS nd
                       LEFT JOIN LATERAL jsonb_array_elements(nd->'stores') AS store ON true
                      WHERE i.id = n.instance_id
                        AND nd->>'type' = n.node_type
                        AND store IS NOT NULL
                   ), ARRAY[]::text[]) AS required_stores,
                   NOW(), 'pending', 'stale', rs.id AS frame_id_placeholder, rs.id
              FROM rimsky_nodes n
              JOIN rimsky_run_scopes rs ON rs.id = $2
             WHERE n.id = $1
               AND NOT EXISTS (
                 SELECT 1 FROM rimsky_node_runs r
                  WHERE r.node_id = $1 AND r.run_scope_id = $2
                    AND r.phase IN ('pending','active','held','parked')
               )`,
           nodeID, runScopeID)
       if err != nil {
           return fmt.Errorf("AffirmNodeRunRow: %w", err)
       }
       return nil
   }
   ```

   **Note on `frame_id` for the inserted row:** the spec keeps `frame_id` on `rimsky_node_runs` (per existing schema). `AffirmNodeRunRow` is called from the cascade walker which holds the current `frame_id`. Decision: extend the helper signature to accept `frameID shared.UUID` so the row is populated correctly. Update the interface to add the parameter:

   ```go
   AffirmNodeRunRow(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, frameID shared.UUID, tx Tx) error
   ```

   Adjust the SQL accordingly: `frame_id` populated from `$3` not from the RunScope id. (The `frame_id_placeholder` comment in the SQL above is a reminder this needs the real frame_id; remove it when wiring through.)

**Verification:**
```
go build ./foundation/persistence/postgres/
```

---

## Task 13 — SQLite impl: `AffirmNodeRunRow`

**Files:** `foundation/persistence/sqlite/nodes.go` (modify)

**Steps:**

1. Mirror Task 12 in SQLite. Use `?` placeholders; substitute SQLite's UUID-generation pattern (or accept the caller-supplied id by extending the helper signature — but Postgres `gen_random_uuid()` is used in `MarkStaleForCascade`'s existing INSERT; SQLite uses a Go-side `uuid.New()` and passes it in. Read `MarkStaleForCascade` in the sqlite impl to match the pattern).

2. Validate the closed-RunScope check via a separate SELECT, same as Task 12 step 1.

**Verification:**
```
go build ./foundation/persistence/sqlite/
```

---

## Task 14 — Add `RunScopeID` projection to `NodeRow`

**Files:** `foundation/persistence/nodes.go` (modify)

**Steps:**

1. Locate `type NodeRow struct` (around line 24). Current shape has `InFlightRunID *shared.UUID` (cycle-1 projection) but does NOT have `ParentRunID` or `ChildKey` as inline fields — those lived on `rimsky_node_runs` directly, and the cycle-1 `NodeRow` projection only added `InFlightRunID` via the postgres LATERAL/CASE projection.

2. Add a new field `RunScopeID *shared.UUID` (pointer because for a node with no in-flight run, the projection yields nil — same pattern as `InFlightRunID`):

   ```go
   // RunScopeID is the RunScope id of the node's current in-flight
   // run (projected via the same LATERAL/CASE that produces
   // InFlightRunID). Nil when no in-flight run exists.
   //
   // @concept: run-scope
   RunScopeID *shared.UUID
   ```

3. The actual column projection extension happens in Tasks 15 + 16 (postgres + sqlite `nodeCols`).

**Verification:**
```
go build ./foundation/persistence/
```
(Build errors in callers reading the removed fields are expected; subsequent tasks address.)

---

## Task 15 — Update postgres `nodeSelect` to project `RunScopeID`

**Files:** `foundation/persistence/postgres/nodes.go` (modify)

**Steps:**

1. Locate `const nodeCols` (or the equivalent column-projection constant) in `foundation/persistence/postgres/nodes.go`.

2. Extend the LATERAL subquery (or CASE projection) that produces `InFlightRunID` to also produce `RunScopeID`:

   ```
   ..., (
     CASE WHEN r.phase IN ('pending','active','held','parked') THEN r.id END
   ) AS in_flight_run_id,
   (
     CASE WHEN r.phase IN ('pending','active','held','parked') THEN r.run_scope_id END
   ) AS in_flight_run_scope_id,
   ...
   ```

   (Or the actual existing projection's shape — adjust to match.)

3. Update `scanNode` to read the additional column into `NodeRow.RunScopeID`.

**Verification:**
```
go build ./foundation/persistence/postgres/
go test ./foundation/persistence/postgres/ -run 'TestNodesGet|TestNodesList' -count=1
```

---

## Task 16 — Update SQLite `nodeSelect` to project `RunScopeID`

**Files:** `foundation/persistence/sqlite/nodes.go` (modify)

**Steps:**

1. Mirror Task 15 in SQLite. The projection shape will differ slightly (SQLite's `CASE WHEN` syntax is the same; the JOIN/scan boilerplate is the SQLite variant).

**Verification:**
```
go build ./foundation/persistence/sqlite/
go test ./foundation/persistence/sqlite/ -run 'TestNodesGet|TestNodesList' -count=1
```

---

## Task 17 — Reshape `DispatchRequest`

**Files:** `foundation/persistence/node_runs.go` (modify); call sites across `runtime/`, `control/controlapi/`, `graph/scheduler/`, `test/scenarios/`, `foundation/persistence/conformance/`.

**Steps:**

1. Open `foundation/persistence/node_runs.go`. Locate `type DispatchRequest struct`.

2. Remove fields `ParentRunID *shared.UUID` and `ChildKey string`. Add `RunScopeID shared.UUID` (non-nullable).

3. Update the struct's docstring to reflect that the dispatch request is scoped to a specific RunScope (caller resolves the RunScope before constructing the request).

**Verification:**
```
go build ./foundation/persistence/
```
Build errors at every `DispatchRequest{}` construction site (per the Pattern B audit: ~22 sites) — expected; addressed in tasks 18–23.

---

## Task 18 — Update `Queue.EnqueueInTx` postgres impl: simplify NOT EXISTS guard

**Files:** `foundation/persistence/postgres/queue.go` (modify)

**Steps:**

1. Locate `EnqueueInTx`. The current implementation has a two-branch NOT EXISTS guard (root vs. child per the cycle-2 fix).

2. Replace with a single-branch guard keyed on `(node_id, run_scope_id, in-flight phases)`:

   ```sql
   INSERT INTO rimsky_node_runs (...)
   SELECT ...
   WHERE NOT EXISTS (
     SELECT 1 FROM rimsky_node_runs
      WHERE node_id = $1 AND run_scope_id = $2
        AND phase IN ('pending','active','held','parked')
   )
   ```

3. Remove the old parent_run_id / child_key parameters; the dispatch request's `RunScopeID` field flows through as a query parameter.

**Verification:**
```
go build ./foundation/persistence/postgres/
```

---

## Task 19 — Update `Queue.EnqueueInTx` SQLite impl

**Files:** `foundation/persistence/sqlite/queue.go` (modify)

**Steps:**

1. Mirror Task 18 in SQLite.

**Verification:**
```
go build ./foundation/persistence/sqlite/
```

---

## Task 20 — Reshape persistence methods to accept `runScopeID` (postgres)

**Files:** `foundation/persistence/postgres/nodes.go`, `foundation/persistence/postgres/queue.go`, `foundation/persistence/postgres/queue_park.go`, `foundation/persistence/postgres/node_attributes.go` (modify)

**Steps:**

For each method below, replace the existing `runID *shared.UUID` disambiguator parameter (or the missing-disambiguator gap) with `runScopeID shared.UUID` (non-nullable). The SELECT keys on `(node_id, run_scope_id, …)` — unambiguous.

- `Nodes.UpdateState`
- `Nodes.UpdateHeartbeat`
- `Nodes.ClearLastOutcome`
- `Nodes.ClearSupervisorAssignment`
- `Nodes.ResetFailedTerminalLastOutcome` (gains the disambiguator; also fix the driver-drift bug — skip the `rimsky_nodes.updated_at` bump when the CTE update affected 0 rows; check the existing impl for the unconditional bump)
- `Queue.GetParkedByNode`
- `Queue.RemoveForNodeInTx`
- `Queue.GetInFlightRunForNode`
- `Queue.SetRetryNoProgressForNodeInTx`
- `NodeAttributes.GetLatestByNode` — **semantic clarification:** the current method returns "the most-recent run's attribute row for the given node" (latest across all runs of the node). Under fan-out where multiple concurrent runs of the same node exist in different RunScopes, "latest across all runs" is ambiguous (which run's attributes?). The reshape adds `runScopeID` to scope the lookup: the new method returns "the most-recent attribute row for this node in this RunScope." Forensic queries that previously read "the latest attributes regardless of scope" must be reshaped to scope-aware queries (caller picks the scope first). This is a semantic change, not just a disambiguation. Update the method's docstring to reflect.

For each method above, remove the optional-pointer disambiguator + the nil branch; the new signature is `(ctx, nodeID, runScopeID, …, tx)`. SELECTs add `AND run_scope_id = $N` to the existing predicate.

**Verification:**
```
go build ./foundation/persistence/postgres/
```

---

## Task 21 — Reshape persistence methods (SQLite mirror)

**Files:** `foundation/persistence/sqlite/nodes.go`, `foundation/persistence/sqlite/queue.go`, `foundation/persistence/sqlite/queue_park.go`, `foundation/persistence/sqlite/node_attributes.go` (modify)

**Steps:**

1. Mirror Task 20 in SQLite for the same method set.

**Verification:**
```
go build ./foundation/persistence/sqlite/
```

---

## Task 22 — Update interface declarations on `NodeTable` / `Queue` / `NodeAttributes`

**Files:** `foundation/persistence/nodes.go`, `foundation/persistence/queue.go`, `foundation/persistence/node_attributes.go` (or wherever the public interfaces are declared)

**Steps:**

1. Reshape each method's signature in the interface declaration to match the new postgres/sqlite impls. Remove `runID *shared.UUID` parameters; replace with `runScopeID shared.UUID`.

2. Update interface docstrings to reflect the new contract (no nil-disambiguator branch; the SELECT is unambiguous by the new unique index).

**Verification:**
```
go build ./foundation/persistence/
go build ./foundation/persistence/postgres/ ./foundation/persistence/sqlite/
```

---

## Task 23 — Reshape `RunTreeTable` (interface)

**Files:** `foundation/persistence/run_tree.go` (modify)

**Steps:**

1. Open `foundation/persistence/run_tree.go`.

2. Update `RunTreeRow`: remove `ParentRunID *shared.UUID` and `ChildKey string`; add `RunScopeID shared.UUID`. Field order matches per scan-order convention.

3. Update `CreateRootRunInput`: add `RunScopeID shared.UUID`. Root run means "first run in its RunScope" — the main RunScope's id flows in here.

4. Update `CreateChildRunInput`: remove `ParentRunID shared.UUID` and `ChildKey string`; add `RunScopeID shared.UUID`. Idempotency moves to `(node_id, run_scope_id)`.

5. Remove `GetByParentChildKey` from the interface. Callers replace with: `RunScopeTable.GetFanoutPartition(parent_run_id, partition_key)` → `Queue.GetInFlightRunForNode(node_id, run_scope_id)`.

6. Reshape `ListChildren`: semantics become "list all in-flight runs in RunScopes whose `parent_run_id = parentRunID`" — implementation joins `rimsky_run_scopes` ON `parent_run_id = $1` with `rimsky_node_runs` ON `run_scope_id`. Signature stays `(ctx, tx, parentRunID) ([]RunTreeRow, error)`.

7. Other methods (`CreateRootRun`, `CreateChildRun`, `GetByID`, `LockTreeForUpdate`, `UpdateStateAndOutcome`, `UpdateAggregationPolicy`) keep their signatures.

**Verification:**
```
go build ./foundation/persistence/
```
Build errors in postgres + sqlite impls — addressed in Tasks 24 + 25.

---

## Task 24 — Postgres impl: `RunTreeTable` reshape

**Files:** `foundation/persistence/postgres/run_tree.go` (modify; or wherever the postgres impl lives)

**Steps:**

1. Read the existing implementation. Update each method's SQL to use `run_scope_id` instead of `parent_run_id` + `child_key`.

2. `ListChildren`: change the query from `WHERE parent_run_id = $1` to a JOIN:

   ```sql
   SELECT nr.id, nr.node_id, nr.frame_id, nr.run_scope_id, nr.state, nr.last_outcome, nr.aggregation_policy
     FROM rimsky_node_runs nr
     JOIN rimsky_run_scopes rs ON rs.id = nr.run_scope_id
    WHERE rs.parent_run_id = $1
   ```

3. Remove `GetByParentChildKey` from the impl entirely.

**Verification:**
```
go build ./foundation/persistence/postgres/
```

---

## Task 25 — SQLite impl: `RunTreeTable` reshape

**Files:** `foundation/persistence/sqlite/run_tree.go` (modify)

**Steps:**

1. Mirror Task 24 in SQLite.

**Verification:**
```
go build ./foundation/persistence/sqlite/
```

---

## Task 26 — Update `RunTreeTable` callers in `runtime/`

**Files:** `runtime/run_tree.go`, `runtime/state_propagation.go`, `runtime/fanout_dispatch.go`, `runtime/subgraph_dispatch.go`, plus any others that grep finds.

**Steps:**

1. Use `grep -rn "RunTree()\." .` AND `grep -rn "GetByParentChildKey" .` to find every caller. The second grep catches internal impl callers + test mocks that the first misses.

2. For each call:
   - `CreateRootRun(...)` / `CreateChildRun(...)` — pass `RunScopeID` from the caller's in-scope RunScope id. Note: `runtime/run_tree.go` is the runtime wrapper; the SQLite `CreateChildRun` impl at `foundation/persistence/sqlite/run_tree.go#79` internally calls `GetByParentChildKey` for idempotency — that internal call gets reshaped to use `(node_id, run_scope_id)` idempotency keying (already part of Task 25).
   - `GetByParentChildKey(parentRunID, childKey)` — production caller at `runtime/run_tree.go:347` replaces with two-step: `args.Persist.RunScopes().GetFanoutPartition(parentRunID, partitionKey)` (or `args.Persist.RunScopes().GetByID(runScopeID)` if known) → `args.Queue.GetInFlightRunForNode(nodeID, runScopeID, ...)`.
   - Test mocks that implement `RunTreeTable` (e.g., `runtime/state_propagation_test.go::fakeRunTreeTable#65` and `test/scenarios/subgraph/exit_carry_rule_test.go::fakeRunTreeTable#51`) need their `GetByParentChildKey` method removed (since the interface drops it). The test logic that called these stubs needs to switch to the two-step pattern OR be removed if no longer applicable.
   - `ListChildren(parentRunID)` — signature unchanged; implementation handles the JOIN.
   - `state_propagation.go::walkUpwards` — at `runtime/state_propagation.go:93` the function is called with `*childRow.ParentRunID` as the seed `current` UUID. Under the reshape, the seed UUID comes from `RunScopeRow.ParentRunID` (looked up via the child's `RunScopeID`): `args.Persist.RunScopes().GetByID(childRow.RunScopeID).ParentRunID`. Then the walk itself uses the same one-hop-via-RunScope pattern.

3. `fanout_dispatch.go::CreateFanOutChildren` (or `PlanFanOutChildren`) — for each child, first create the fanout_partition RunScope (`args.Persist.RunScopes().Create(...)`), then create the child run within it.

**Verification:**
```
go build ./runtime/
```

---

## Task 27 — Update `DispatchRequest` constructors

**Files:** every site found by `grep -rn "persistence\.DispatchRequest{" .` and `grep -rn "DispatchRequest{" runtime/ control/ graph/ test/`

**Steps:**

1. For each `DispatchRequest{...}` literal, remove `ParentRunID` and `ChildKey` (if present) and add `RunScopeID` from the caller's in-scope RunScope id.

2. Per the Pattern B audit, the production sites are:
   - `runtime/runner_error_policy.go` (retry + give_up + infra branches — all 3 sites already thread parent context; reshape to thread RunScopeID)
   - `runtime/on_error.go` (retry branch — currently doesn't thread parent context; reshape to thread RunScopeID via the new `OnErrorArgs.RunScopeID` field — see Task 41)
   - `runtime/conductor.go::SweepStaleHeartbeats` and `SweepReady` (re-enqueue paths; thread RunScopeID from `NodeRow.RunScopeID`)
   - `runtime/cascade_recalculate.go::RecalculateNode` (thread RunScopeID from `target.RunScopeID`)
   - `graph/scheduler/pure_cascade.go` (root-only dispatches; pass main RunScope id)
   - Test fixtures + conformance: pass a valid RunScope id seeded in the test

3. Test fixtures may need a fresh `RunScope` row created as part of the fixture setup (otherwise `RunScopeID` is invalid).

**Verification:**
```
go build ./...
```
Build clean across all modules.

---

## Task 27.5 — Reshape `acquisition` struct in runtime

**Files:** `runtime/runner_acquire.go` (modify)

**Steps:**

1. Open `runtime/runner_acquire.go`. Locate the `acquisition` struct (around line 99-130). Current shape includes `ChildKey string` and `ParentRunID *shared.UUID` (from cycle-1 + cycle-2 work).

2. Remove `ChildKey` and `ParentRunID` fields. Add `RunScopeID shared.UUID` (non-nullable, since every run lives in some RunScope).

3. Update the acquisition-population sites in the same file:
   - At `tryAcquire`'s happy-path literal (around line 444), where `parentRunID` and `childKey` are populated from the `RunTreeRow` fetched via `RunTree.GetByID(cand.DispatchID)`: now populate `RunScopeID` from the same row's reshaped `RunScopeID` field (per Task 23 — `RunTreeRow.RunScopeID` replaces the inline parent/child fields).
   - At the unavailable-branch literal (around line 420), same.

4. Search for `acq.ChildKey` and `acq.ParentRunID` across the codebase; replace each with appropriate scope-based lookups (e.g., where the old code needed `acq.ParentRunID`, the new code reads `args.Persist.RunScopes().GetByID(acq.RunScopeID).ParentRunID`; where it needed `acq.ChildKey`, it reads `.PartitionKey` from the same).

**Verification:**
```
go build ./runtime/
grep -rn "acq\\.ChildKey\\|acq\\.ParentRunID" runtime/ control/ graph/
# Should return nothing.
```

---

## Task 27.6 — Extend `InstanceCreateInput` + Create impls for `main_run_scope_id`

**Files:** `foundation/persistence/instances.go` (interface struct), `foundation/persistence/postgres/instances.go`, `foundation/persistence/sqlite/instances.go` (impls)

**Steps:**

1. Add `MainRunScopeID shared.UUID` to `InstanceCreateInput` (`foundation/persistence/instances.go::InstanceCreateInput#97`). Required (non-nullable; populated by the handler before Create is called).

2. Update `InstanceRow` (sibling struct) to project `MainRunScopeID shared.UUID`.

3. In `foundation/persistence/postgres/instances.go`, update the `Create` method's INSERT statement to include `main_run_scope_id`; pass `in.MainRunScopeID` as a parameter. Update `scanInstance` to scan the new column.

4. Mirror in `foundation/persistence/sqlite/instances.go`.

5. The migration adding the column is Task 33.5; the handler that populates it is Task 33.

**Verification:**
```
go build ./foundation/persistence/
go build ./foundation/persistence/postgres/ ./foundation/persistence/sqlite/
```

---

## Task 28 — Conformance test: RunScope lifecycle

**Files:** `foundation/persistence/conformance/run_scope_lifecycle.go` (new); `foundation/persistence/conformance/conformance.go` (modify — register subtest)

**Steps:**

1. Create the file with these subtests:
   - `testRunScopeCreate_MainAndChild` — create a main RunScope; create a child RunScope under it; assert FK constraints; assert `parent_run_scope_id` and `parent_run_id` set or unset per the CHECK constraint.
   - `testRunScopeClose_StampsClosedAt` — create a RunScope; call `Close`; assert `closed_at` is set; call `Close` again; assert idempotent.
   - `testRunScopeAffirmAfterClose_ErrRunScopeClosed` — create a RunScope; close it; call `AffirmNodeRunRow` against it; assert returns `ErrRunScopeClosed`.
   - `testRunScopeFanoutPartitionUniqueness` — create two fanout_partition RunScopes with the same `(parent_run_id, partition_key)`; assert the second insert fails the unique constraint (or returns the existing one, depending on implementation choice).
   - `testRunScopeListParentChain` — create a chain of 3 RunScopes; call `ListParentChain` from the leaf; assert returns all 3 in order.

2. Register in `conformance.go::Suite` with `t.Run` entries pointing to the new functions.

**Verification:**
```
go test ./foundation/persistence/postgres/... -run 'TestSuite/RunScope' -count=1
go test ./foundation/persistence/sqlite/...   -run 'TestSuite/RunScope' -count=1
```

---

## Task 29 — Conformance test: `AffirmNodeRunRow`

**Files:** `foundation/persistence/conformance/affirm_node_run_row.go` (new); `foundation/persistence/conformance/conformance.go` (modify)

**Steps:**

1. Create the file with these subtests:
   - `testAffirmNodeRunRow_InsertsWhenNoInFlight` — empty state; call affirm; assert exactly one row exists.
   - `testAffirmNodeRunRow_Idempotent` — call affirm twice; assert still exactly one row.
   - `testAffirmNodeRunRow_ErrorsOnClosedScope` — close the RunScope; call affirm; assert `ErrRunScopeClosed` returned.
   - `testAffirmNodeRunRow_NoReturnValueDependency` — confirm the signature returns only `error` (compile-time check; the test imports the method and uses it in a way that would fail to build if the return shape changed).
   - `testAffirmThenRead` — call affirm; call `GetInFlightRunForNode`; assert returns the affirmed row's id and `phase = 'pending'`, `state = 'stale'`.

2. Register in `conformance.go::Suite`.

**Verification:**
```
go test ./foundation/persistence/postgres/... -run 'TestSuite/AffirmNodeRunRow' -count=1
go test ./foundation/persistence/sqlite/...   -run 'TestSuite/AffirmNodeRunRow' -count=1
```

---

## Task 30 — Conformance test: in-flight lookup by `(node_id, run_scope_id)`

**Files:** `foundation/persistence/conformance/run_in_flight_lookup.go` (new); `foundation/persistence/conformance/conformance.go` (modify)

**Steps:**

1. Create with subtests:
   - `testInFlightLookup_SingleRowPerScopePerNode` — seed an in-flight row; call `GetInFlightRunForNode(nodeID, runScopeID)`; assert returns the row.
   - `testInFlightLookup_NoFalsePositiveAcrossScopes` — seed two RunScopes sharing the same node_id, each with their own in-flight row; assert each scope's lookup returns its own row.
   - `testInFlightLookup_ReturnsNoneWhenAbsent` — empty state; lookup returns nil.

2. Register.

**Verification:**
```
go test ./foundation/persistence/postgres/... -run 'TestSuite/RunInFlightLookup' -count=1
go test ./foundation/persistence/sqlite/...   -run 'TestSuite/RunInFlightLookup' -count=1
```

---

## Task 31 — Conformance test: state writes isolated by RunScope

**Files:** `foundation/persistence/conformance/run_state_writes_isolated_by_scope.go` (new); `foundation/persistence/conformance/conformance.go` (modify)

**Steps:**

1. Create with subtests verifying that for each of `UpdateState`, `UpdateHeartbeat`, `ClearLastOutcome`, `ClearSupervisorAssignment`, `ResetFailedTerminalLastOutcome`, `RemoveForNodeInTx`, `GetParkedByNode`, `SetRetryNoProgressForNodeInTx`, `NodeAttributes.GetLatestByNode`:
   - Seed runs in two RunScopes A and B sharing a node_id.
   - Call the method with `runScopeID = A`.
   - Assert the row in scope B is unchanged.

2. The existing fan-out / disambiguator conformance tests (`nodes_update_state_fanout_run_id.go`, `nodes_clear_fanout_run_id.go`, `queue_remove_for_node_fanout_run_id.go`, `queue_enqueue_fanout_partition.go`, `queue_in_flight_run_for_node_fanout.go`) are retired in Task 32; this new test replaces their coverage.

3. Register in `conformance.go::Suite`.

**Verification:**
```
go test ./foundation/persistence/postgres/... -run 'TestSuite/RunStateWritesIsolated' -count=1
go test ./foundation/persistence/sqlite/...   -run 'TestSuite/RunStateWritesIsolated' -count=1
```

---

## Task 32 — Retire superseded conformance tests

**Files:** `foundation/persistence/conformance/nodes_update_state_fanout_run_id.go`, `foundation/persistence/conformance/nodes_clear_fanout_run_id.go`, `foundation/persistence/conformance/queue_remove_for_node_fanout_run_id.go`, `foundation/persistence/conformance/queue_enqueue_fanout_partition.go`, `foundation/persistence/conformance/queue_in_flight_run_for_node_fanout.go`, `foundation/persistence/conformance/queue_parked_by_node_run_id.go` (delete); `foundation/persistence/conformance/conformance.go` (modify — remove the retired subtest registrations)

**Steps:**

1. `git rm` each retired file (or plain `rm` if untracked).

2. Remove the corresponding `t.Run("...", func(t *testing.T) { testXxx(t, factory(t)) })` lines from `conformance.go::Suite`.

**Verification:**
```
go build ./foundation/persistence/conformance/
go test ./foundation/persistence/postgres/... -run 'TestSuite' -count=1
```
Build clean; suite runs without the retired tests; new tests from Tasks 28–31 take their place.

---

## Task 33 — Create main RunScope at instance creation

**Files:** `control/controlapi/instances.go` (modify)

**Steps:**

1. Find the `POST /instances` handler (likely `handleCreateInstance` or `createInstance`). It currently inserts the instance row.

2. In the same tx as the instance insert, create the main RunScope:

   ```go
   mainScopeID := shared.UUID(uuid.New())
   if err := args.Persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
       ID:               mainScopeID,
       ParentRunScopeID: nil,
       ParentRunID:      nil,
       GraphName:        spec.MainGraphName,  // "main"
       PartitionKey:     "",
       InstanceID:       instanceID,
   }); err != nil {
       return fmt.Errorf("create main run scope: %w", err)
   }
   ```

3. The main RunScope's id is persisted on the instance row via `rimsky_instances.main_run_scope_id` (added in Task 33.5 below). The handler reads/writes this column.

4. Update `code:foundation/persistence.InstanceRow` to project `MainRunScopeID shared.UUID`; update postgres + sqlite scans accordingly. The migration that adds the column is Task 33.5.

5. Update the `InstanceCreateInput` struct to accept the `MainRunScopeID shared.UUID` field; the create-instance handler computes it (`shared.UUID(uuid.New())`), inserts the RunScope, then inserts the instance with this column set.

**Verification:**
```
go build ./...
go test ./control/controlapi/ -count=1
```

---

## Task 33.5 — Migration 010: add `rimsky_instances.main_run_scope_id`

**Files:** `foundation/persistence/postgres/migrations/010-instances-main-run-scope.sql` (new); `foundation/persistence/sqlite/migrations/010-instances-main-run-scope.sql` (new)

**Steps:**

1. Postgres migration:

   ```sql
   -- =====  rimsky_instances.main_run_scope_id  =====
   -- Persist the main RunScope id on the instance row so handlers
   -- (operator queries, callback resolution, etc.) can look up the
   -- main scope without scanning rimsky_run_scopes. Per spec
   -- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
   -- §Lifecycle / Main RunScope.
   ALTER TABLE rimsky_instances
       ADD COLUMN main_run_scope_id UUID NOT NULL REFERENCES rimsky_run_scopes(id);
   ```

2. SQLite parallel: same structure with TEXT id.

   Note: SQLite's `ALTER TABLE ADD COLUMN NOT NULL` (without DEFAULT) fails on a populated table. Pre-v1 break-freely covers this — the dev SQLite database is dropped/recreated before re-running migrations, so populated-table-ADD-NOT-NULL doesn't arise. If for some reason the migration needs to run against a populated database, add `DEFAULT '00000000-0000-0000-0000-000000000000'` (a sentinel) and follow up with code that backfills real values.

**Verification:**
```
test -f foundation/persistence/postgres/migrations/010-instances-main-run-scope.sql
test -f foundation/persistence/sqlite/migrations/010-instances-main-run-scope.sql
```

---

## Task 34 — Create sub-graph RunScope in `applyTerminalCompleteSubgraphCaller`

**Files:** `runtime/subgraph_dispatch.go` (modify)

**Steps:**

1. Open `runtime/subgraph_dispatch.go`. Locate `applyTerminalCompleteSubgraphCaller` (around line 397 per spec).

2. In the same tx as the internal cascade firing, create the sub-graph RunScope:

   ```go
   subgraphScopeID := shared.UUID(uuid.New())
   if err := args.Persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
       ID:               subgraphScopeID,
       ParentRunScopeID: &acq.RunScopeID,
       ParentRunID:      &acq.DispatchID,
       GraphName:        acq.NodeDef.Delegate,
       PartitionKey:     "",
       InstanceID:       acq.InstanceID,
   }); err != nil {
       return fmt.Errorf("create subgraph run scope: %w", err)
   }
   ```

3. The internal cascade (`SubgraphParentSuccessCascade`) needs to know the sub-graph's RunScope id to allocate runs in it. Pass `subgraphScopeID` through.

**Verification:**
```
go build ./runtime/
go test ./runtime/ -run TestApplyTerminalCompleteSubgraphCaller -count=1
```

---

## Task 35 — Create fanout_partition RunScopes in `AcquireSubClaims`

**Files:** `runtime/runner_subclaim.go` (modify)

**Steps:**

1. Open `runtime/runner_subclaim.go`. Locate `AcquireSubClaims` (around line 126).

2. **First extend `AcquireSubClaimsInput`.** Read the current struct at `runtime/runner_subclaim.go:45-61`. Current fields: `ParentClaimHandleID`, `ParentScope`, `ProducerName`, `NodeRunID`, `HolderNodeID`, `HolderSupervisorID`, `FrameID`, `PartitionRequest`, `Lifetime`, `HeartbeatInterval`, `AggregationPolicy`. Add three new fields:

   ```go
   // ParentRunScopeID is the calling node's RunScope (the parent of
   // the new fanout_partition scopes). Required for run-scope-tree.
   ParentRunScopeID shared.UUID

   // InstanceID is the rimsky_instances row that owns the run.
   // Threaded onto the new RunScope rows.
   InstanceID shared.UUID

   // ParentGraphName is the calling node's graph name; the new
   // fanout_partition scopes inherit it (children run in the same
   // graph as the parent — fan-out doesn't cross graph boundaries).
   ParentGraphName string
   ```

3. **Thread these from the caller** in `runner_acquire_helpers.go::acquireFanOutIfDeclared`. The acquisition struct already carries `RunScopeID`, `InstanceID`, and the graph name (or can derive them from the node spec).

4. **Extend the `SubClaim` struct** (the return type from `AcquireSubClaims`) to carry `RunScopeID shared.UUID` so the dispatcher can thread it into the child's `DispatchRequest`.

5. **In the same tx as the sub-claim handle inserts, create a fanout_partition RunScope per partition descriptor:**

   ```go
   for i, sub := range subClaims {
       partitionScopeID := shared.UUID(uuid.New())
       if err := args.Persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
           ID:               partitionScopeID,
           ParentRunScopeID: &input.ParentRunScopeID,
           ParentRunID:      &input.NodeRunID,
           GraphName:        input.ParentGraphName,
           PartitionKey:     sub.PartitionKey,
           InstanceID:       input.InstanceID,
       }); err != nil {
           return nil, fmt.Errorf("create fanout partition run scope: %w", err)
       }
       subClaims[i].RunScopeID = partitionScopeID  // thread to the child dispatch
   }
   ```

**Verification:**
```
go build ./runtime/
go test ./runtime/ -run TestAcquireSubClaims -count=1
```

---

## Task 36 — Close sub-graph RunScope in carry-rule

**Files:** `runtime/subgraph_dispatch.go` (modify)

**Steps:**

1. Locate the carry-rule (`CarryExitWriteback` or similar — search `grep -n "CarryExitWriteback\|carry" runtime/subgraph_dispatch.go`).

2. In the same tx as the writeback copy, add `args.Persist.RunScopes().Close(ctx, tx, exitRun.RunScopeID)`.

**Verification:**
```
go build ./runtime/
go test ./runtime/ -run TestCarryExitWriteback -count=1
```

---

## Task 37 — Close fanout_partition RunScopes in aggregation walk

**Files:** `runtime/auto_terminal_chain.go` (modify)

**Steps:**

1. Locate `resolveParentClaimChain` in `runtime/auto_terminal_chain.go` (NOT `auto_terminal.go` — verified by grep; the function lives in the `_chain.go` sibling file).

2. When the aggregation walker visits a fan-out parent and reads its children's outcomes, close each partition RunScope in the same tx:

   ```go
   for _, child := range children {
       if err := args.Persist.RunScopes().Close(ctx, tx, child.RunScopeID); err != nil {
           return fmt.Errorf("close partition run scope: %w", err)
       }
   }
   ```

**Verification:**
```
go build ./runtime/
go test ./runtime/ -run TestResolveParentClaimChain -count=1
```

---

## Task 38 — Reshape cascade walker

**Files:** `runtime/runner_terminal.go` (modify — `cascadeSubscribersStaleInTx`, `pullHardDepUpstreams`)

**Steps:**

1. Open `runtime/runner_terminal.go`. Locate `cascadeSubscribersStaleInTx`.

2. Replace the current "snapshot-then-mutate-then-re-fetch" pattern with the affirm-then-read pattern per spec §"Cascade walker reshape":

   ```go
   // For each subscriber edge:
   targetScopeID := senderScopeID  // same-scope is the common case
   // (cross-scope cases — sub-graph entry-success, fan-out parent
   //  settlement — handled by the caller setting targetScopeID before
   //  calling into this loop)
   if err := args.Persist.Nodes().AffirmNodeRunRow(ctx, receiver.ID, targetScopeID, senderFrameID, tx); err != nil {
       return fmt.Errorf("affirm receiver run: %w", err)
   }
   receiverRow, err := args.Queue.GetInFlightRunForNode(ctx, receiver.ID, targetScopeID, tx)
   if err != nil { return err }
   if receiverRow == nil {
       continue  // race-with-terminal; safe to skip
   }
   if receiverRow.Phase == "parked" {
       // explicit chain into wake (cascade walker's policy)
       if err := wakeParkedReceiverInTx(ctx, args, tx, receiverRow, senderFrameID); err != nil {
           return err
       }
       // re-read; wake may have transitioned state
       receiverRow, err = args.Queue.GetInFlightRunForNode(ctx, receiver.ID, targetScopeID, tx)
       if err != nil { return err }
   }
   if err := args.Persist.Nodes().MarkStaleForCascade(ctx, receiverRow.ID, senderFrameID, tx); err != nil {
       return err
   }
   if err := args.Persist.WaitSet().Insert(ctx, tx, persistence.WaitSetRow{
       SenderRunID:   senderRunID,
       ReceiverRunID: receiverRow.ID,
       FrameID:       senderFrameID,
       // ... other wait-set fields
   }); err != nil {
       return err
   }
   ```

3. Apply the same shape to `pullHardDepUpstreams` (with the upstream-scope computation specific to hard-dep traversal).

**Verification:**
```
go build ./runtime/
go test ./runtime/ -run 'TestCascadeSubscribers|TestPullHardDepUpstreams' -count=1
```

---

## Task 39 — Simplify `MarkStaleForCascade` (drop insert path)

**Files:** `foundation/persistence/postgres/nodes.go`, `foundation/persistence/sqlite/nodes.go`, `foundation/persistence/nodes.go` (interface) — modify

**Steps:**

1. The current `MarkStaleForCascade` has both an insert path (when no in-flight row exists) and an update path (when one does). With `AffirmNodeRunRow` owning allocation, the insert path becomes dead code.

2. Simplify the implementation to a pure UPDATE keyed by `run_id`:

   ```go
   // MarkStaleForCascade transitions the run's state to 'stale' and
   // pins frame_id. Pure UPDATE; allocation is the cascade walker's
   // job via AffirmNodeRunRow.
   //
   // @blessed-invariant: State-machine writes for a single run must be
   // tx-atomic.
   //
   // @concept: cascade
   MarkStaleForCascade(ctx context.Context, runID shared.UUID, frameID shared.UUID, tx Tx) error
   ```

3. Update both backends: postgres + sqlite. The signature changes from `(nodeID, frameID, tx) (bool, error)` to `(runID, frameID, tx) error` — note the parameter name change and the return type simplification (no more `(inserted bool, ...)` since insert is gone).

4. Update the cascade walker (Task 38) to pass the resolved `runID` (from `GetInFlightRunForNode`) into `MarkStaleForCascade`.

**Verification:**
```
go build ./...
go test ./foundation/persistence/... ./runtime/ -count=1
```

---

## Task 40 — Add `Nodes.GetRunByDispatchIDForUpdate`

**Files:** `foundation/persistence/nodes.go` (interface), `foundation/persistence/postgres/nodes.go`, `foundation/persistence/sqlite/nodes.go` (impls)

**Steps:**

1. Add to the `NodeTable` interface:

   ```go
   // GetRunByDispatchIDForUpdate returns the in-flight rimsky_node_runs
   // row for the given dispatch_id, with SELECT ... FOR UPDATE row lock.
   // Returns nil if the row doesn't exist (run was reaped or never
   // existed). Used by the callback handler in runtime/callback.go to
   // resolve the run for a callback under the atomic phase check.
   //
   // @blessed-invariant: Callback determinism per spec.
   GetRunByDispatchIDForUpdate(ctx context.Context, dispatchID shared.UUID, tx Tx) (*NodeRunRow, error)
   ```

2. (Note: `NodeRunRow` may need to exist as a fuller projection than `NodeRow`. If `RunTreeRow` covers the needed fields including `Phase`, the method returns `*RunTreeRow` instead — check what `applyTerminal` needs and pick the right shape.)

3. Implement in postgres + sqlite as a `SELECT ... WHERE id = $1 FOR UPDATE`. SQLite uses `BEGIN IMMEDIATE` semantics — the row lock is implicit when the tx is in immediate mode.

**Verification:**
```
go build ./...
```

---

## Task 41 — Reshape callback path: determinism rule + RunScope-first resolution

**Files:** `runtime/callback.go` (modify)

**Steps:**

1. Open `runtime/callback.go`. Locate `driveTerminal` (around line 362).

2. Replace the current `populateAcquisitionLineageFields`-based resolution with the new pattern per spec §"Callback determinism / Implementation shape":

   ```go
   err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
       row, err := args.Persist.Nodes().GetRunByDispatchIDForUpdate(ctx, dispatchID, tx)
       if err != nil { return err }
       if row == nil {
           args.Logger.Warn("callback.late_or_stale_run",
               "dispatch_id", dispatchID,
               "reason", "run_not_found")
           return nil  // ack-but-noop
       }
       if row.Phase != "active" && row.Phase != "held" {
           args.Logger.Warn("callback.late_or_stale_run",
               "dispatch_id", dispatchID,
               "current_phase", row.Phase,
               "expected_phase", "active|held")
           return nil  // ack-but-noop
       }
       return applyTerminal(ctx, row, terminal, tx)
   })
   ```

3. Construct an `acquisition` from the row's fields (the row carries `run_scope_id`, `node_id`, `instance_id` via FK chain; resolve as needed for `applyTerminal`'s shape).

4. Retire `populateAcquisitionLineageFields` (or update it to do the simpler RunScope-first lookup). The cycle-3 best-effort behavior (silently default nil ParentRunID) is gone — under RunScope-first, `RunScopeID` is non-null per schema.

**Verification:**
```
go build ./runtime/
go test ./runtime/ -run TestDriveTerminal -count=1
```

---

## Task 42 — HTTP callback ack body: structured response

**Files:** `runtime/callback.go` (modify)

**Steps:**

1. Define a Go struct for the response body:

   ```go
   type callbackAckBody struct {
       AckStatus         string  `json:"ack_status"`  // "accepted" | "rejected_run_terminal" | "rejected_run_stale" | "rejected_run_parked" | "rejected_unknown"
       CurrentDispatchID *string `json:"current_dispatch_id,omitempty"`
   }
   ```

2. The callback handler writes this body (JSON) with HTTP 200 status for both accepted and rejected callbacks per the ack-but-noop discipline.

3. On rejection, compute `current_dispatch_id` from the canonical successor via RunScope-id lookup: walk to the run's RunScope; look up the current in-flight run for the same node in the same RunScope; if non-nil, that's the successor.

**Verification:**
```
go build ./runtime/
go test ./runtime/ -run TestCallbackResponseBody -count=1
```

---

## Task 43 — Recovery-aware executor protocol: proto changes

**Files:** `protocols/proto/v1/executor.proto` (modify); `protocols/proto/v1/gen/` (regenerate via `make proto-gen`)

**Steps:**

1. Open `protocols/proto/v1/executor.proto`. Locate `ExecuteRequest`.

2. Add two new optional fields. Pick field numbers that don't collide with existing ones in `ExecuteRequest` (verify by reading the current proto). For illustration assume next free numbers are N and N+1:

   ```proto
   message ExecuteRequest {
     // ... existing fields ...

     // prior_dispatch_id is set when this dispatch supersedes a prior
     // failed/abandoned dispatch for the same RunScope+node. Used by
     // executors maintaining per-dispatch session state for recovery
     // handoff. See concept:run-scope and the recovery-aware protocol
     // section of the design spec.
     optional string prior_dispatch_id = N;

     enum PriorDispatchDisposition {
       PRIOR_NONE = 0;
       PRIOR_HEARTBEAT_STALE = 1;
       PRIOR_RETRY_AFTER_ERROR = 2;
       PRIOR_RECALCULATE = 3;
     }
     optional PriorDispatchDisposition prior_dispatch_disposition = N+1;
   }
   ```

3. Run `make proto-gen` to regenerate Go bindings.

**Verification:**
```
make proto-gen
go build ./...
```

---

## Task 44 — Populate recovery-aware fields at supervisor dispatch sites

**Files:** `runtime/conductor.go`, `runtime/runner_error_policy.go`, `runtime/on_error.go`, `runtime/cascade_recalculate.go` (modify)

**Steps:**

1. At each site that constructs an `ExecuteRequest` to send to the executor, populate `prior_dispatch_id` and `prior_dispatch_disposition` when the dispatch is a recovery / retry / recalculate:

   - `conductor.go::SweepStaleHeartbeats`: when re-enqueueing the recovered run, the new dispatch carries `prior_dispatch_id = old_run.dispatch_id`, `prior_dispatch_disposition = PRIOR_HEARTBEAT_STALE`.
   - `runner_error_policy.go::applyResolvedAction` retry branch: `prior_dispatch_id = acq.DispatchID`, `prior_dispatch_disposition = PRIOR_RETRY_AFTER_ERROR`.
   - `on_error.go::OnError` retry branch: same as above.
   - `cascade_recalculate.go::RecalculateNode` (when re-dispatching): `prior_dispatch_id = prior_run.dispatch_id`, `prior_dispatch_disposition = PRIOR_RECALCULATE`.
   - Initial dispatch (root run creation): both fields unset.

2. The actual `ExecuteRequest` construction site is wherever the dispatcher hands off to the executor (likely `runtime/runner_dispatch.go::buildExecuteRequest` or similar). Verify the construction is centralized; if it's spread across multiple sites, the recovery context flows through `acquisition` or a similar carrier struct.

**Verification:**
```
go build ./...
go test ./runtime/ -run 'TestRecoveryAwareDispatch|TestPriorDispatchPopulated' -count=1
```

---

## Task 45 — ParkReason proto collapse (7 → 2)

**Files:** `protocols/proto/v1/executor.proto` (modify); `protocols/proto/v1/gen/` (regenerate)

**Steps:**

1. Open `protocols/proto/v1/executor.proto`. Locate `enum ParkReason` (around line 208).

2. Replace the seven values with two. Per pre-v1 break-freely, the values' numeric ids can be reset:

   ```proto
   enum ParkReason {
     // No UNSPECIFIED, no OTHER, no TIME_WAIT/SIGNAL_WAIT/AWAITING_HUMAN/
     // RETRY_BACKOFF/CALLBACK_WAIT. Closed two-value set per spec
     // .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
     //
     // Note: proto3 requires a zero value. We pick the more common case
     // (AWAIT_CALLBACK) as 1 and SNOOZE as 2, and declare 0 as invalid
     // — wire decode rejects.
     PARK_REASON_AWAIT_CALLBACK = 1;
     PARK_REASON_SNOOZE         = 2;
   }
   ```

   Actually proto3 enums require 0 to be defined. Two options:
   - Define `PARK_REASON_AWAIT_CALLBACK = 0` (sensible default).
   - Keep an explicit `PARK_REASON_INVALID = 0` and document it as invalid wire value.

   Decision per spec: pick `PARK_REASON_AWAIT_CALLBACK = 0` (default to the more conservative choice — executor that forgets to set the field gets the wait-on-callback interpretation, which won't auto-resume).

3. Update `ParkTerminal` message:

   ```proto
   message ParkTerminal {
     ParkReason reason = 1;
     optional google.protobuf.Timestamp resume_at = 2;  // required iff reason == SNOOZE
     optional string reason_label = 3;  // optional informational; opaque to rimsky
   }
   ```

4. Run `make proto-gen`.

**Verification:**
```
make proto-gen
go build ./...
```

---

## Task 46 — ParkReason: schema CHECK constraint + dead-code removal

**Files:** `foundation/persistence/postgres/migrations/011-park-reason-collapse.sql` (new); `foundation/persistence/sqlite/migrations/011-park-reason-collapse.sql` (new); `runtime/runner_terminal_park.go` (modify)

**Steps:**

1. Create postgres migration 011:

   ```sql
   -- =====  parked_reason CHECK collapse  =====
   -- Per spec, ParkReason is now a closed two-value enum.
   -- Schema CHECK enforces parity.
   ALTER TABLE rimsky_node_runs
       DROP CONSTRAINT IF EXISTS rimsky_node_runs_parked_reason_check;
   UPDATE rimsky_node_runs SET parked_reason = 'await_callback'
       WHERE parked_reason IN ('signal_wait', 'awaiting_human', 'callback_wait');
   UPDATE rimsky_node_runs SET parked_reason = 'snooze'
       WHERE parked_reason IN ('time_wait', 'retry_backoff');
   UPDATE rimsky_node_runs SET parked_reason = 'await_callback'
       WHERE parked_reason IN ('unspecified', 'other');  -- safe default for migration
   ALTER TABLE rimsky_node_runs
       ADD CONSTRAINT rimsky_node_runs_parked_reason_check
       CHECK (parked_reason IS NULL OR parked_reason IN ('await_callback', 'snooze'));
   ```

2. Create sqlite migration 011 (mirror).

3. Open `runtime/runner_terminal_park.go::applyTerminalPark`. The cycle-2 in-code rejection of `PARK_REASON_UNSPECIFIED` (which returned an error without releasing the in-flight row) becomes dead code — the proto wire layer catches the bad value before the handler runs. Delete the rejection branch.

**Verification:**
```
test -f foundation/persistence/postgres/migrations/011-park-reason-collapse.sql
test -f foundation/persistence/sqlite/migrations/011-park-reason-collapse.sql
go build ./runtime/
```

---

## Task 47 — `OnError`: hoist `requiredStoresForNode` out of outer tx

**Files:** `runtime/on_error.go` (modify)

**Steps:**

1. Open `runtime/on_error.go`. Locate the retry branch's outer tx wrap (the cycle-2 fix that bundles remove + enqueue).

2. The current shape calls `requiredStoresForNode` inside the closure; that function opens a nested `sb.Transaction`, deadlocking SQLite's pool.

3. Hoist the call out of the closure. Note: `requiredStoresForNode` returns only `[]string` (no error) — verify the actual signature at `runtime/on_error.go:268`:

   ```go
   // Compute required stores BEFORE entering the outer tx.
   // (requiredStoresForNode internally opens its own sb.Transaction —
   //  if called inside the outer tx, the nested Transaction blocks
   //  forever on the SQLite single-conn pool.)
   requiredStores := requiredStoresForNode(ctx, sb, nd)

   err = sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
       // ... use the captured requiredStores variable inside the closure ...
   })
   ```

**Verification:**
```
go build ./runtime/
go test ./runtime/ -race -run 'TestOnError' -count=3
```

---

## Task 48 — `OnError`: same-tx atomicity for read + write

**Files:** `runtime/on_error.go` (modify)

**Steps:**

1. The cycle-3 audit found two CROSS-TX-SPLIT issues in `OnError`:
   - The `EvaluatorState` read happens in one tx; the state mutation in another. Race window between.
   - The give_up branch's `RemoveForNode` is auto-commit, outside the tx that updated state. If RemoveForNode fails, the row is stranded at failed state.

2. Bundle both into the outer tx:
   - Read `EvaluatorState` inside the outer tx (move the read into the closure, immediately before the state mutation).
   - For the give_up branch, use `RemoveForNodeInTx` (the tx-accepting variant) instead of the auto-commit `RemoveForNode`. The cycle-2 retry branch already uses `RemoveForNodeInTx`; give_up should too.

3. Add the `@blessed-invariant: State-machine tx atomicity` annotation block above `OnError`:

   ```go
   // @blessed-invariant: State-machine writes for a single run must be
   // tx-atomic. Any operation that reads a run's current state to
   // decide what state to write must perform the read and the write
   // in the same transaction. Per spec
   // .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
   ```

**Verification:**
```
go build ./runtime/
go test ./runtime/ -race -run 'TestOnError' -count=3
```

---

## Task 49 — Reconcile `invalidateInFrame` frame_id atomicity

**Files:** `runtime/cascade_invalidate.go` (modify)

**Steps:**

1. Open `runtime/cascade_invalidate.go`. Locate `invalidateInFrame` (around lines 217-241 per spec).

2. The current code resolves `frame_id` outside the mutating tx to avoid a SQLite deadlock with `invalidateNextFrame`. The atomicity gap is real (frame_id can stale between resolve and mutate); the fix is structural rather than a one-line move.

3. Restructure per spec §"Remaining explicit fixes / fix #5":
   - Hoist the `invalidateNextFrame` fallback path out of the in-tx code path (it was the source of the deadlock).
   - Inside the mutating tx, do a fresh read of the source node's current `frame_id`. If it matches the resolved value, proceed; if it differs (stale), abort the mutation cleanly and let the calling cascade walker retry from a fresh resolve.

4. Add the `@blessed-invariant: State-machine tx atomicity` annotation.

**Verification:**
```
go build ./runtime/
go test ./runtime/ -race -run 'TestInvalidateInFrame' -count=3
```

---

## Task 50 — `IncrementAttributeOverrideMatchCounts` WARN contract reconciliation

**Files:** `foundation/persistence/instances.go` (modify)

**Steps:**

1. Open `foundation/persistence/instances.go`. Locate the `IncrementAttributeOverrideMatchCounts` interface docstring (around lines 67-85 per spec).

2. The docstring promises "WARN observability for out-of-range indices" but neither postgres nor sqlite impl emits one (per cycle-3 audit fix #6).

3. Decision per spec: update the docstring to reflect actual silent-no-op behavior. Replace the WARN-promising sentence with: "Out-of-range indices are silently no-op'd at the persistence layer; observability surface is the application-layer caller. The runtime's `incrementMatchCountersAfterMerge` Warn-logs failures of the entire call but does not enumerate per-index drops."

4. No code change to the impls — the behavior is preserved; only the contract docstring updates to match reality.

**Verification:**
```
go build ./foundation/persistence/
grep -n "observability surface is the application-layer caller" foundation/persistence/instances.go
```

---

## Task 50.5 — `@blessed-invariant`: State-machine tx atomicity at `applyResolvedAction`

**Files:** `runtime/runner_error_policy.go` (modify)

**Steps:**

1. Add the same `@blessed-invariant: State-machine writes for a single run must be tx-atomic` annotation above `applyResolvedAction` (per the spec — even though the function is already correctly structured, the annotation is the regression pin).

   ```go
   // @blessed-invariant: State-machine writes for a single run must be
   // tx-atomic. Any operation that reads a run's current state to
   // decide what state to write must perform the read and the write
   // in the same transaction. Per spec
   // .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
   ```

**Verification:**
```
grep -B1 "@blessed-invariant: State-machine" runtime/runner_error_policy.go
```

---

## Task 51 — `@blessed-invariant`: AffirmNodeRunRow no-return-value-dependency

**Files:** `foundation/persistence/nodes.go` (modify — already part of Task 11's docstring, verify)

**Steps:**

1. The annotation was added in Task 11. Verify it's present at the AffirmNodeRunRow declaration with the exact phrasing per spec §"Design changes / New invariants".

**Verification:**
```
grep -A2 "@blessed-invariant: AffirmNodeRunRow" foundation/persistence/nodes.go
```

---

## Task 52 — `@blessed-invariant`: Callback determinism

**Files:** `runtime/callback.go` (modify)

**Steps:**

1. Add the annotation block above `driveTerminal`:

   ```go
   // @blessed-invariant: A callback for a run is honored if and only
   // if the run's phase ∈ {active, held} at acceptance, checked
   // atomically inside the same tx as the state mutation. Per spec
   // .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
   // §"Callback determinism".
   ```

**Verification:**
```
grep -B1 "@blessed-invariant.*A callback for a run" runtime/callback.go
```

---

## Task 53 — `@blessed-invariant`: Park terminal closed-enum

**Files:** `protocols/proto/v1/executor.proto` (modify — add as a comment above the enum)

**Steps:**

1. Add a comment block above `enum ParkReason`:

   ```proto
   // @blessed-invariant: ParkReason is a closed set of two values
   // (AWAIT_CALLBACK and SNOOZE). The proto wire layer rejects any
   // other value at decode. No UNSPECIFIED, no OTHER, no fallback.
   // Per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
   ```

**Verification:**
```
grep -B1 "@blessed-invariant: ParkReason" protocols/proto/v1/executor.proto
```

---

## Task 54 — Recovery-aware protocol unit test (TS executor)

**Files:** `executors/claude-agent/src/recovery_aware.test.ts` (new)

**Steps:**

1. Create a unit test in the TS executor that verifies:
   - `ExecuteRequest` parsing reads `prior_dispatch_id` and `prior_dispatch_disposition` fields.
   - HTTP response body parsing reads `ack_status` and optionally `current_dispatch_id`.

   Use the existing TS test patterns in `executors/claude-agent/src/` as the structural template.

**Verification:**
```
cd executors/claude-agent && npm test -- recovery_aware
```

---

## Task 55 — Conformance test: recovery-aware dispatch population

**Files:** `foundation/persistence/conformance/recovery_aware_dispatch.go` (new); `foundation/persistence/conformance/conformance.go` (modify); harness extension in `graph/scenario/harness.go` for recording dispatched ExecuteRequests

**Steps:**

1. The scenario harness already integrates an executor stub at `executors/stub/stub.go` that records every received `ExecuteRequest` via `Stub.Observed()` (returns a snapshot). The harness's `Stub *stubexec.Stub` field exposes it. Use this existing recording — no new harness work needed.

2. Create the conformance test:
   - Seed a fan-out parent with one partition.
   - Simulate heartbeat-stale recovery: dispatch the child, stop heartbeat, let sweep transition to recovery, observe the new dispatch's `ExecuteRequest`.
   - Assert `prior_dispatch_id` matches the original child's dispatch_id and `prior_dispatch_disposition == PRIOR_HEARTBEAT_STALE`.

3. Register in `conformance.go::Suite`.

**Verification:**
```
go test ./foundation/persistence/postgres/... -run 'TestSuite/RecoveryAwareDispatch' -count=1
```

---

## Task 56 — State-machine tx atomicity test for `OnError`

**Files:** `runtime/on_error_tx_atomicity_test.go` (new)

**Steps:**

1. Create a test that hooks the tx mechanism with a test double counting opens. Assert that `OnError`'s retry path opens exactly one tx for the read-and-write sequence (not two).

   Use the existing tx test infrastructure if present; otherwise mock the `persistence.Tx` interface for the test.

**Verification:**
```
go test ./runtime/ -run TestOnErrorTxAtomicity -count=1
```

---

## Task 57 — Must-pass E2E scenario F1: fan-out success + cascade

**Files:** `test/scenarios/fanout_success_cascade_e2e_test.go` (new)

**Steps:**

1. Create the scenario per spec §"Test coverage matrix / F1":
   - Register a template with a fan-out parent + a downstream main-graph subscriber.
   - Fan-out emits three partitions.
   - All children execute to success.
   - Aggregation settles parent.
   - Downstream subscriber receives cascade.
   - Wait-set drains correctly.
   - Assert: each partition's RunScope is closed; main-RunScope's downstream subscriber's run reaches `state = fresh_changed` after parent settlement.

2. Use the existing scenario harness pattern (`code:graph/scenario/harness.go`) for setup.

**Verification:**
```
go test ./test/scenarios/ -run TestFanOutSuccessCascadeE2E -count=1
```

---

## Task 58 — Must-pass E2E scenario F2: fan-out child error + retry

**Files:** `test/scenarios/fanout_child_error_retry_e2e_test.go` (new)

**Steps:**

1. Per spec §F2: one fan-out child errors; retry policy fires; child re-dispatches in the same partition RunScope; recovery-aware protocol fields populate.

2. Assertions:
   - `prior_dispatch_id` is set on the retried dispatch.
   - `prior_dispatch_disposition == PRIOR_RETRY_AFTER_ERROR`.
   - The child eventually reaches success terminal.
   - The retry stays within the same partition RunScope (not reassigned to a new scope).
   - Parent aggregation settles on the eventual outcome.

**Verification:**
```
go test ./test/scenarios/ -run TestFanOutChildErrorRetryE2E -count=1
```

---

## Task 59 — Must-pass E2E scenario F3: fan-out heartbeat-stale recovery

**Files:** `test/scenarios/fanout_heartbeat_stale_recovery_e2e_test.go` (new)

**Steps:**

1. Per spec §F3: one child's supervisor "dies" mid-execution; sweep transitions; new supervisor dispatches successor with `prior_dispatch_id` set.

2. Test simulates heartbeat death by stopping the heartbeat tick on a specific child run. Wait for `SweepStaleHeartbeats` to fire.

3. Assertions:
   - New dispatch carries `prior_dispatch_id = old_run.dispatch_id`.
   - `prior_dispatch_disposition == PRIOR_HEARTBEAT_STALE`.
   - New run lives in the same partition RunScope.

**Verification:**
```
go test ./test/scenarios/ -run TestFanOutHeartbeatStaleRecoveryE2E -count=1
```

---

## Task 60 — Must-pass E2E scenario F4: fan-out callback determinism

**Files:** `test/scenarios/fanout_callback_determinism_e2e_test.go` (new)

**Steps:**

1. Per spec §F4: child dispatched, parks (AWAIT_CALLBACK), external callback completes; second callback for the same dispatch_id arrives after the first was processed; second is rejected per the determinism rule.

2. Assertions:
   - First callback: HTTP 200, body `ack_status = "accepted"`.
   - Second callback: HTTP 200, body `ack_status = "rejected_run_terminal"` (the run is now in a terminal phase after first callback applied).
   - No double-state-mutation visible in event log.

**Verification:**
```
go test ./test/scenarios/ -run TestFanOutCallbackDeterminismE2E -count=1
```

---

## Task 61 — Must-pass E2E scenarios S1–S4: sub-graph

**Files:** `test/scenarios/subgraph_internal_cascade_e2e_test.go`, `test/scenarios/subgraph_exit_carry_e2e_test.go`, `test/scenarios/subgraph_internal_error_retry_e2e_test.go`, `test/scenarios/subgraph_cascade_through_exit_e2e_test.go` (new — 4 files)

**Steps:**

1. **S1** (`subgraph_internal_cascade_e2e_test.go`): calling node succeeds → sub-graph RunScope created → internal cascade propagates → internal nodes dispatch in sub-graph RunScope.
2. **S2** (`subgraph_exit_carry_e2e_test.go`): exit terminates with writeback → carry-rule fires → calling node's writeback receives exit's output → sub-graph RunScope closes.
3. **S3** (`subgraph_internal_error_retry_e2e_test.go`): internal node errors → retry stays within sub-graph RunScope → exit eventually terminates.
4. **S4** (`subgraph_cascade_through_exit_e2e_test.go`): outer node fires → cascade traverses to a node downstream of exit → correctly traces back through carry-rule to the calling node.

**Verification:**
```
go test ./test/scenarios/ -run 'TestSubgraph(InternalCascade|ExitCarry|InternalErrorRetry|CascadeThroughExit)E2E' -count=1
```

---

## Task 62 — Concept doc: create `concepts/run-scope.md`

**Files:** `.ok-planner/design/concepts/run-scope.md` (new)

**Steps:**

1. Create the file with full content per spec §"Design changes / New concept". The file follows the standard concept-doc template (front-matter, `## What it is`, `## Purpose`, `## Boundaries`, `## Invariants`, `## Annotation sites`, `## Notes`).

2. Concrete content from the spec's enumeration:

   ```markdown
   ---
   concept: run-scope
   status: as-is
   aliases: []
   references:
     - ../../specs/2026-05-22-fan-out-safety-scope-first-design.md
   ---

   # RunScope

   ## What it is

   RunScope is the first-class execution context for one graph instantiation (main / subgraph / fanout_partition). Persisted as `rimsky_run_scopes`. Each RunScope owns a set of `rimsky_node_runs` rows (the **RunSheet** in operator prose). RunScopes form a tree via `parent_run_scope_id`.

   Three kinds:
   - **Main RunScope:** the top-level graph instantiation. One per instance. No parent.
   - **Sub-graph RunScope:** a sub-graph invoked via a calling node's `delegate:`. Parent = the calling node's RunScope; parent run = the calling node's run.
   - **Fan-out partition RunScope:** one per partition emitted by a fan-out node's `SplitScope`. Parent = the fan-out node's RunScope; parent run = the fan-out node's run; carries a non-empty `partition_key`.

   Kind is derivable, not stored: `parent_run_scope_id IS NULL` → main; `partition_key != ''` → fanout_partition; else subgraph.

   ## Purpose

   Uniform representation of execution contexts; eliminates the bug class of inline-disambiguator drift (parent_run_id + child_key on rimsky_node_runs); enables depth-gating via parent-chain walks (complementing canonicalizer-level recursion rejection per `concept:sub-graph` as runtime defense-in-depth); enables agentic-executor recovery handoff via the `prior_dispatch_id` / `current_dispatch_id` protocol.

   ## Boundaries

   Owns: the per-RunScope `rimsky_node_runs` set; RunScope lifecycle (creation / closure); parent-RunScope / parent-run relationships.

   Does NOT own: claim semantics (parallel structure via `concept:claim-tree`); cascade-edge semantics (`concept:cascade` traverses subscription edges within and across RunScopes); frame semantics (frames and RunScopes are orthogonal).

   Adjacent: `concept:fan-out`, `concept:delegation`, `concept:frame`, `concept:claim-tree`, `concept:cascade`, `concept:node-run`.

   ## Invariants

   - RunScope rows inserted eagerly in the tx that triggers them: main at instance creation; subgraph at calling-node success terminal (`code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller`); fanout_partition at SplitScope sub-claim acquisition (`code:runtime/runner_subclaim.go::AcquireSubClaims`) per `@blessed-invariant 10`.
   - `parent_run_scope_id IS NULL ⇔ parent_run_id IS NULL ⇔ main RunScope`. Enforced by the table's CHECK constraint.
   - `partition_key != ''` iff fanout_partition; uniqueness of open fanout_partition per `(parent_run_id, partition_key)` enforced by `uq_run_scopes_fanout_partition_open`.
   - `closed_at IS NOT NULL` means parent-run rendezvous has fired (sub-graph carry-rule, fan-out aggregation, or instance termination). `AffirmNodeRunRow` returns `ErrRunScopeClosed`. Cascade walker reaching INTO a closed RunScope is a bug.
   - `AffirmNodeRunRow` is the lazy-allocation primitive; callers must not depend on its return value beyond error/no-error (preserves lazy↔eager rewrite property).
   - Depth gating: runtime safety net that rejects a sub-graph creating a RunScope already present in the parent chain at any depth. The canonicalizer's static `subgraph_recursion_unsupported` rejection per `concept:sub-graph` is the primary; this is defense-in-depth.

   ## Annotation sites

   - `code:foundation/persistence/postgres/run_scopes.go`, `code:foundation/persistence/sqlite/run_scopes.go` — backend impls.
   - `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller` — sub-graph RunScope creation.
   - `code:runtime/runner_subclaim.go::AcquireSubClaims` — fan-out partition RunScope creation.
   - `code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx` — cascade walker carries RunScope.
   - `code:runtime/callback.go::driveTerminal` — callback resolves RunScope via dispatch_id.

   ## Notes

   - 2026-05-22 — Created per spec `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`.
   ```

**Verification:**
```
test -f .ok-planner/design/concepts/run-scope.md
grep "^concept: run-scope" .ok-planner/design/concepts/run-scope.md
```

---

## Task 63 — Concept doc rename: `scope.md` → `claim-scope.md`

**Files:** `.ok-planner/design/concepts/scope.md` (delete after content moves); `.ok-planner/design/concepts/claim-scope.md` (new)

**Steps:**

1. Create `.ok-planner/design/concepts/claim-scope.md` by copying the content of `scope.md` with the following in-file mutations:
   - Front-matter: `concept: scope` → `concept: claim-scope`; `aliases: []` → `aliases: [scope (pre-2026-05-22, retired)]`.
   - Title: `# Scope` → `# Claim Scope`.
   - `## What it is`: rewrite to use "claim scope" throughout. Replace "Scope is the opaque byte stream..." → "ClaimScope is the opaque byte stream a `ClaimProducer.Open` returns to identify 'what was acquired.' Persisted as `col:rimsky_claim_handles.claim_scope_data`. Compared byte-equally via `code:foundation/locks/conflict.go::ClaimScopesByteEqual`."
   - `### Selector vs scope` subsection: rename to `### Selector vs claim scope`. Replace all "scope" → "claim scope" in the body. Update the substitution directive line: `{{claim.<alias>.scope}}` → `{{claim.<alias>.claim_scope}}`.
   - `## Purpose`: use "claim scope" throughout.
   - `## Boundaries`: use "claim scope"; update column reference to `claim_scope_data`.
   - `## Invariants`: use "claim scope" throughout.
   - `## Aliases and historical names`: append "Renamed from `scope` to `claim-scope` per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md, to disambiguate from `concept:run-scope` (the execution-context concept)."
   - `## Common pitfalls`: keep the JS/AWS/OAuth-scope disambiguation; remove or update the "Rimsky's scope is not [other scopes]" framing since ClaimScope's name is self-disambiguating now.
   - `## Notes`: append `2026-05-22 — Renamed from concept:scope to concept:claim-scope per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md to make room for concept:run-scope.`

2. Delete `.ok-planner/design/concepts/scope.md`.

**Verification:**
```
test -f .ok-planner/design/concepts/claim-scope.md
test ! -f .ok-planner/design/concepts/scope.md
```

---

## Task 64 — Mutate `concepts/fan-out.md`

**Files:** `.ok-planner/design/concepts/fan-out.md` (modify)

**Steps:**

1. Update Definition: "Each child leaf run gets `parent_run_id = parent's run id`, `child_key = <partition_key>`" → "Each child runs in its own fan-out partition RunScope (per `concept:run-scope`), with `parent_run_id = fan-out parent's run id`, `parent_run_scope_id = fan-out parent's RunScope id`, `partition_key = <partition_key>`. The child's leaf run lives in this RunScope."

2. Update Invariants line that documents the parent_run_id + child_key shape — same replacement as above.

3. Update Boundaries — add: "Owns the SplitScope-driven RunScope creation at parent acquisition; does NOT own RunScope semantics in general (see `concept:run-scope`)."

4. Append Notes entry: `2026-05-22 — Reshape per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md: fan-out children now live in fan-out partition RunScopes (concept:run-scope) rather than carrying inline parent_run_id + child_key on the node_run row.`

**Verification:**
```
grep "fan-out partition RunScope" .ok-planner/design/concepts/fan-out.md
```

---

## Task 65 — Mutate `concepts/delegation.md`

**Files:** `.ok-planner/design/concepts/delegation.md` (modify)

**Steps:**

1. Update the "asymmetric identity" paragraphs:
   - Entry-absorbed: stays structurally the same; the sub-graph internal nodes now live in a sub-graph RunScope with `parent_run_id = calling node's run id`, `parent_run_scope_id = calling node's RunScope id`. The calling node's run remains in the parent RunScope.
   - Exit-writeback carry-rule: fires at exit's leaf-run terminal, atomically with sub-graph RunScope closure (`closed_at` set).

2. Update Annotation sites — reference `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller` for sub-graph RunScope creation.

3. Append Notes: `2026-05-22 — Reshape per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md: sub-graph internal nodes now live in a sub-graph RunScope (concept:run-scope); carry-rule closure semantics added.`

**Verification:**
```
grep "sub-graph RunScope" .ok-planner/design/concepts/delegation.md
```

---

## Task 66 — Mutate `concepts/cascade.md`

**Files:** `.ok-planner/design/concepts/cascade.md` (modify)

**Steps:**

1. Update the body's walker description to note: the cascade walker carries `run_scope_id` through subscription edges; the `AffirmNodeRunRow` primitive owns row allocation; `MarkStaleForCascade` simplifies to a pure UPDATE keyed by `run_id` (no insert path).

2. Update the 2026-05-14 Notes entry's description of "wait-set rows are inserted on every cascade-walk match" to mention the affirm-then-read pattern.

3. Append Notes: `2026-05-22 — Reshape per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md: cascade walker is RunScope-aware; AffirmNodeRunRow is the allocation primitive; MarkStaleForCascade is a pure UPDATE.`

**Verification:**
```
grep "AffirmNodeRunRow is the allocation primitive" .ok-planner/design/concepts/cascade.md
```

---

## Task 67 — Mutate `concepts/frame.md`

**Files:** `.ok-planner/design/concepts/frame.md` (modify)

**Steps:**

1. Add a clarifying note: "Frames and RunScopes (per `concept:run-scope`) are orthogonal: a single cascade frame can span multiple RunScopes (cascade propagation across sub-graph entry-success or fan-out parent settlement); a single RunScope can host multiple frames (the same graph firing across multiple cascade resolutions)."

2. Append Notes: `2026-05-22 — Clarified orthogonality with concept:run-scope per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`

**Verification:**
```
grep "Frames and RunScopes" .ok-planner/design/concepts/frame.md
```

---

## Task 68 — Mutate `concepts/node-run.md`

**Files:** `.ok-planner/design/concepts/node-run.md` (modify)

**Steps:**

1. In `## What it is`:
   - Remove `parent_run_id UUID NULL` and `child_key TEXT NULL` from the column list.
   - Add `run_scope_id UUID NOT NULL` with the note: "FK to `rimsky_run_scopes` (per `concept:run-scope`). All scoping — parent/child relationship for fan-out, sub-graph membership for delegation — is now expressed through this FK chain rather than inline on the node_run row."
   - Keep `aggregation_policy JSONB NULL`.

2. Rewrite the `**Run-tree**` paragraph: "node-runs form a tree via `parent_run_id` + `child_key`. A root run has both columns NULL..." → "node-runs are organized into RunScopes (per `concept:run-scope`) via `run_scope_id`. The tree shape that previously lived inline on the node_run row now lives on `rimsky_run_scopes` via `parent_run_scope_id`. Walking the RunScope tree from a leaf RunScope to the main RunScope recovers the full execution stack. State aggregation walks bottom-up through the RunScope tree."

3. Update `## Boundaries` to reference `concept:run-scope` as adjacent.

4. Append Notes: `2026-05-22 — Reshape per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md: parent_run_id and child_key removed from rimsky_node_runs; replaced by run_scope_id (FK to rimsky_run_scopes). Run-tree shape moves to concept:run-scope.`

**Verification:**
```
grep "run_scope_id UUID NOT NULL" .ok-planner/design/concepts/node-run.md
```

---

## Task 69 — Mutate `concepts/claim-tree.md`

**Files:** `.ok-planner/design/concepts/claim-tree.md` (modify)

**Steps:**

1. Update the Definition's parenthetical: "(which uses `parent_run_id` + `child_key`)" → "(which uses `run_scope_id` per `concept:run-scope`, with the parent-child shape on `rimsky_run_scopes` rather than inline on the node_run row)."

2. Append Notes: `2026-05-22 — Updated cross-reference to reflect the run-tree shape change per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md. Claim-tree (parent_claim_handle_id on rimsky_claim_handles) and RunScope-tree (parent_run_scope_id on rimsky_run_scopes) are now both first-class trees at the persistence layer; they remain parallel structures owned by different concepts.`

**Verification:**
```
grep "run_scope_id per .concept:run-scope" .ok-planner/design/concepts/claim-tree.md
```

---

## Task 70 — Mutate `concepts/parked-state.md`

**Files:** `.ok-planner/design/concepts/parked-state.md` (modify)

**Steps:**

1. Update Common pitfalls: replace the 4-reason enum citation (`TIME_WAIT / CALLBACK_WAIT / RETRY_BACKOFF / OTHER`) with the new 2-reason set (`AWAIT_CALLBACK / SNOOZE`).

2. Update the 2026-05-14 / 2026-05-15 Notes entries that describe the typed-enum taxonomy: keep them as historical record. Append a 2026-05-22 entry that supersedes the prior taxonomy.

3. Update executor mapping guidance:
   - `long-running-job → CALLBACK_WAIT` becomes `long-running-job → AWAIT_CALLBACK`.
   - `time-based polling → TIME_WAIT` and `rate-limit-aware → RETRY_BACKOFF` both become `SNOOZE`.
   - `awaiting-human → CALLBACK_WAIT` becomes `awaiting-human → AWAIT_CALLBACK`.

4. Update per-reason `max_park_duration` defaults: document the new two-reason defaults. Suggested: AWAIT_CALLBACK unbounded (or 24h max); SNOOZE capped at `resume_at + grace_window`.

5. Append Notes: `2026-05-22 — ParkReason enum collapsed from 7 values to 2 (AWAIT_CALLBACK, SNOOZE) per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md. PARK_REASON_UNSPECIFIED and PARK_REASON_OTHER removed entirely; TIME_WAIT/RETRY_BACKOFF map to SNOOZE; SIGNAL_WAIT/AWAITING_HUMAN/CALLBACK_WAIT map to AWAIT_CALLBACK.`

**Verification:**
```
grep "AWAIT_CALLBACK, SNOOZE" .ok-planner/design/concepts/parked-state.md
```

---

## Task 71 — Mutate `concepts/attribute.md`

**Files:** `.ok-planner/design/concepts/attribute.md` (modify)

**Steps:**

1. Update the L5 matcher overlay invariant to reflect that the matcher's `child_key` key sources from the run's RunScope's `partition_key` (per `concept:run-scope`), not from a column on the node_run row.

2. Update the `## Matcher overlay (by_match)` section: add the sentence "Under RunScope-first (per spec 2026-05-22), the `child_key` matcher key sources its value from the dispatched run's RunScope's `partition_key`; the equality semantics and ordinal-rejection vocabulary remain unchanged."

3. Append Notes: `2026-05-22 — child_key matcher anchor sourcing reconciled per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md: matcher reads from RunScope's partition_key now that parent_run_id + child_key are removed from rimsky_node_runs.`

**Verification:**
```
grep "RunScope's .partition_key" .ok-planner/design/concepts/attribute.md
```

---

## Task 72 — Mutate `concepts/claim-handle.md`

**Files:** `.ok-planner/design/concepts/claim-handle.md` (modify)

**Steps:**

1. Update Columns section: `lock_kind ∈ {named, scope}` → `lock_kind ∈ {named, claim_scope}`; `scope_data` → `claim_scope_data`.

2. Append Notes: `2026-05-22 — Updated for ClaimScope rename per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`

**Verification:**
```
grep "lock_kind .* {named, claim_scope}" .ok-planner/design/concepts/claim-handle.md
```

---

## Task 73 — Mutate `concepts/inertness.md`

**Files:** `.ok-planner/design/concepts/inertness.md` (modify)

**Steps:**

1. Update all "scope" mentions in the claim-identity sense to "claim scope"; update `concept:scope` adjacency reference to `concept:claim-scope`; update invariant references to use the qualified name.

2. Append Notes: `2026-05-22 — Updated for ClaimScope rename per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`

**Verification:**
```
grep "concept:claim-scope" .ok-planner/design/concepts/inertness.md
```

---

## Task 74 — Mutate `concepts/lineage-record.md`

**Files:** `.ok-planner/design/concepts/lineage-record.md` (modify)

**Steps:**

1. Update `scope_data_hash` references to `claim_scope_data_hash`.

2. Update the `leaf_run` and `claim_terminal` projections to reflect:
   - The column rename (`scope_data` → `claim_scope_data`).
   - The new sourcing of `partition_key` / `parent_run_id` for run-tree-bearing projections — those values come from joining `rimsky_node_runs.run_scope_id → rimsky_run_scopes` rather than from inline columns.

3. Append Notes: `2026-05-22 — Updated for ClaimScope rename and run-tree reshape per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`

**Verification:**
```
grep "claim_scope_data_hash" .ok-planner/design/concepts/lineage-record.md
```

---

## Task 75 — Mutate `concepts/claim.md`

**Files:** `.ok-planner/design/concepts/claim.md` (modify)

**Steps:**

1. Update `concept:scope` adjacency → `concept:claim-scope`.

2. Update "byte-equal scope" references → "byte-equal claim scope".

3. Append Notes entry.

**Verification:**
```
grep "concept:claim-scope" .ok-planner/design/concepts/claim.md
```

---

## Task 76 — Mutate `concepts/claim-producer.md`

**Files:** `.ok-planner/design/concepts/claim-producer.md` (modify)

**Steps:**

1. Update "scope bytes" references → "claim scope bytes".

2. Update `concept:scope` adjacency → `concept:claim-scope`.

3. Append Notes entry.

**Verification:**
```
grep "claim scope bytes" .ok-planner/design/concepts/claim-producer.md
```

---

## Task 77 — Mutate `concepts/write-semantics.md`

**Files:** `.ok-planner/design/concepts/write-semantics.md` (modify)

**Steps:**

1. Update "byte-equal scope" references → "byte-equal claim scope".

2. Update `concept:scope` adjacency → `concept:claim-scope`.

3. Append Notes entry.

**Verification:**
```
grep "byte-equal claim scope" .ok-planner/design/concepts/write-semantics.md
```

---

## Task 78 — Fix `tensions/_resolved/region-vs-scope-legacy.md` frontmatter

**Files:** `.ok-planner/design/tensions/_resolved/region-vs-scope-legacy.md` (modify)

**Steps:**

1. Fix the frontmatter: `status: open` → `status: resolved` (the file is in `_resolved/` but the frontmatter is wrong).

2. Append a Notes line noting the ClaimScope rename per this spec is consistent with the original resolution's qualified-naming spirit.

**Verification:**
```
grep "^status: resolved" .ok-planner/design/tensions/_resolved/region-vs-scope-legacy.md
```

---

## Task 79 — Regenerate `concepts.md` TOC

**Files:** `.ok-planner/design/concepts.md` (modify)

**Steps:**

1. After the renames and additions, regenerate the TOC. The format per `code:ok-planner:discover-design`'s SKILL.md is a sorted alphabetical bullet list of `<slug> — <one-sentence definition>` with optional `(aliases: ...)` parenthetical.

2. Update the bullet for the renamed concept: `claim-scope` replaces the prior `scope` entry; one-sentence definition derives from the new `claim-scope.md` "What it is" section.

3. Add a new bullet for `run-scope`: one-sentence definition derives from `run-scope.md`.

4. Confirm bullets are sorted alphabetically; the `claim-scope` bullet should sit between `claim-tree` and `claim-handle` per alpha sort; `run-scope` sits among the `r*` bullets.

**Verification:**
```
grep -E '^- `claim-scope` ' .ok-planner/design/concepts.md
grep -E '^- `run-scope` ' .ok-planner/design/concepts.md
# Bare `scope` should be gone — use word-anchored grep to avoid false positives on claim-scope / run-scope:
if grep -E '^- `scope` ' .ok-planner/design/concepts.md ; then
  echo "ERROR: bare 'scope' TOC entry still present" && exit 1
fi
```

---

## Task 80 — Code rename: `ScopesByteEqual` → `ClaimScopesByteEqual`

**Files:** `foundation/locks/conflict.go` (modify); callers of `ScopesByteEqual` across the codebase

**Steps:**

1. Rename the function in `foundation/locks/conflict.go`.

2. `grep -rn "ScopesByteEqual" .` and rename every caller.

**Verification:**
```
go build ./...
grep -r "ScopesByteEqual" --include='*.go' | grep -v ClaimScopesByteEqual
# Should return nothing.
```

---

## Task 81 — Code rename: `ClaimResult.Scope` → `ClaimResult.ClaimScope`

**Files:** `protocols/claimproducer/types.go` (modify); callers across the codebase

**Steps:**

1. Rename the Go struct field in `protocols/claimproducer/types.go`.

2. `grep -rn "ClaimResult.*\.Scope\b\|\.Scope\b.*ClaimResult" .` and rename every caller. Be careful not to false-positive on other `.Scope` references (e.g., `acquiredLocks[i].ClaimResult.Scope` is the load-bearing one).

**Verification:**
```
go build ./...
```

---

## Task 81.5 — Code rename: `ClaimHandleTable.UpdateScope` → `UpdateClaimScope`

**Files:** `foundation/persistence/claim_handles.go` (interface); `foundation/persistence/postgres/claim_handles.go`, `foundation/persistence/sqlite/claim_handles.go` (impls); callers — `runtime/runner_acquire_claims.go`, `foundation/persistence/conformance/conformance.go`, `foundation/persistence/conformance/claim_handles_update_scope.go`, `foundation/persistence/sqlite/deadlock_guard_test.go`, plus anything else `grep` finds.

**Steps:**

1. Rename the method on the interface and both backend impls. The SQL inside (`UPDATE rimsky_claim_handles SET claim_scope_data = ...`) already uses the renamed column from Task 5/6.

2. `grep -rn "\\.UpdateScope\\b\\|UpdateScope(" .` to find every caller and rename.

**Verification:**
```
go build ./...
grep -r "UpdateScope" --include='*.go' . | grep -v UpdateClaimScope
# Should return nothing.
```

---

## Task 81.6 — Code rename: `ClaimHandleRow.ScopeData` → `ClaimScopeData` (Go struct fields)

**Files:** `foundation/persistence/claim_handles.go` (struct); plus 20+ callers across `runtime/runner_held_claims.go`, `runtime/runner_acquire_claims.go`, `runtime/runner_locks.go`, `runtime/auto_terminal.go`, `runtime/auto_terminal_chain.go`, `runtime/runner_terminal_release.go`, `runtime/terminal_decision_cancel.go`, `runtime/runner_subclaim.go`, `runtime/instance_termination.go`, plus `CreateInput`/`UpdateInput` shapes.

**Steps:**

1. Rename `ClaimHandleRow.ScopeData` → `ClaimScopeData`. Update the JSON tag (if any) to `claim_scope_data` to match the renamed column.

2. Rename `CreateInput.ScopeData` (or whatever the input struct's field is called) similarly.

3. `grep -rn "\\.ScopeData\\b" .` to find every caller and rename. Be careful not to false-positive on `SubScopeDescriptor.ScopeData` — that's a separate field on a different type (handled in Task 81.8).

**Verification:**
```
go build ./...
# Confirm only intentional non-renamed references remain:
grep -r "ScopeData" --include='*.go' . | grep -v ClaimScopeData | grep -v SubScopeDescriptor
# Should return nothing (or only known intentional exclusions).
```

---

## Task 81.7 — Update `runtime/lineage_writer.go` for ClaimScope + RunScope reshape

**Files:** `runtime/lineage_writer.go` (modify); `foundation/persistence/postgres/lineage.go`, `foundation/persistence/sqlite/lineage.go` (modify the filter predicates that read dropped columns)

**Steps:**

1. In `runtime/lineage_writer.go`:
   - Rename `ScopeDataHash` field → `ClaimScopeDataHash` (and the JSON tag to `claim_scope_data_hash`) per ClaimScope rename.
   - The `ParentRunID` and `ChildKey` fields (lines ~92, 94 per cycle-3 reviewer's finding) source from `rimsky_node_runs.parent_run_id` / `child_key` — which are dropped. Reshape: source from the run's RunScope via `args.Persist.RunScopes().GetByID(run.RunScopeID)` → `RunScopeRow.ParentRunID` and `RunScopeRow.PartitionKey`. The lineage JSON projection retains the same field names (`parent_run_id`, `child_key`) for back-compat with existing forensic queries — only the source changes.

2. In `foundation/persistence/postgres/lineage.go` (around line 128) and `foundation/persistence/sqlite/lineage.go` (around line 127): filter predicates that key on `record->>'parent_run_id'` are unaffected as long as the lineage JSON still carries that field. Verify by reading the existing query; if it filters on `rimsky_node_runs.parent_run_id` (a now-dropped column) directly, reshape to use the new RunScope join.

**Verification:**
```
go build ./...
go test ./runtime/ -run 'TestLineage' -count=1
go test ./foundation/persistence/postgres/ -run 'TestLineage' -count=1
```

---

## Task 81.8 — Decide `SubScopeDescriptor` Go type naming

**Files:** `foundation/locks/types.go` (modify — `SubScopeDescriptor`, `SplitScopeRequest`, `SplitScopeResponse`); `foundation/locks/storetest/fake.go` (modify — `SplitScopeFunc`); 15+ callers across `runtime/runner_subclaim.go`, `runtime/remote/client.go`, `runtime/clientiface/data_processing.go`, `cmd/rimsky-data-processing-conformance/checks.go`, `test/smoke/`, `test/scenarios/`, `runtime/runner_subclaim_test.go`.

**Steps:**

1. The following types/functions use "Scope" as part of the type name in the claim-bytes sense. Under the ClaimScope discipline, rename:
   - `foundation/locks/types.go::SubScopeDescriptor` (and its `ScopeData` field) → `SubClaimScopeDescriptor` (and `ClaimScopeData`)
   - `foundation/locks/types.go::SplitScopeRequest` → `SplitClaimScopeRequest`
   - `foundation/locks/types.go::SplitScopeResponse` → `SplitClaimScopeResponse`
   - `foundation/locks/storetest/fake.go::SplitScopeFunc` (note: this is a `FakeStore` field at line 56, NOT in `types.go`) → `SplitClaimScopeFunc`
   - The proto-side rename in Task 82 (`ScopesConflictRequest` → `ClaimScopesConflictRequest`) is the parallel discipline.

2. `grep -rn "SubScopeDescriptor\\|SplitScopeRequest\\|SplitScopeResponse\\|SplitScopeFunc" .` and rename every caller. Test sites include `runtime/runner_subclaim_test.go:219`.

**Verification:**
```
go build ./...
grep -r "SubScopeDescriptor\\|SplitScopeRequest\\|SplitScopeResponse\\|SplitScopeFunc" --include='*.go' .
# Should return nothing.
```

---

## Task 81.10 — Code rename: additional "Scope" claim-bytes identifiers

**Files:** `foundation/persistence/database.go`, `foundation/persistence/claim_handles.go`, `runtime/runner_acquire_claims.go`, `runtime/runner_held_claims.go`, `runtime/runner_subclaim.go`, plus test files

**Steps:**

This catches the Go identifiers using "Scope" in the claim-bytes sense that Tasks 80–81.9 did not enumerate. Without these renames the ClaimScope discipline is incomplete and Audit G (Task 88) would pass while the inconsistency remains.

Rename:

- `foundation/persistence/database.go::TakeScopeLockInTx#84` → `TakeClaimScopeLockInTx` (and update all callers)
- `foundation/persistence/claim_handles.go::ClaimHandleTable.ListByProducerScope#198` → `ListByProducerClaimScope` (and callers — primarily `runtime/runner_acquire_claims.go::evaluateScopeConflict#177`)
- `runtime/runner_acquire_claims.go::evaluateScopeConflict#173` → `evaluateClaimScopeConflict`
- `runtime/runner_subclaim.go::AcquireSubClaimsInput.ParentScope#47` → `ParentClaimScope`
- `runtime/runner_held_claims.go::matchesScope#212` (and the `scopeData` parameter) → `matchesClaimScope` (and `claimScopeData`)
- Test file rename: `runtime/scope_conflict_committed_durable_test.go` → `runtime/claim_scope_conflict_committed_durable_test.go`; rename test functions inside (e.g., `TestScopeConflict_CommittedDurableStillConflicts` → `TestClaimScopeConflict_CommittedDurableStillConflicts`).
- Test file rename: `test/scenarios/locks/scope_conflict_race_test.go` → `test/scenarios/locks/claim_scope_conflict_race_test.go`; rename test functions inside similarly.

Also update Audit G (Task 88) grep pattern to include these new identifiers — see Task 88 update below.

**Verification:**
```
go build ./...
grep -r "TakeScopeLockInTx\\|ListByProducerScope\\|evaluateScopeConflict\\|ParentScope\\|matchesScope\\|scopeData\\b" --include='*.go' .
# Should return nothing (or only intentional non-ClaimScope-related matches; review carefully).
```

---

## Task 81.9 — Retire/rename conformance test files using `scope` in filenames

**Files:** `foundation/persistence/conformance/scope.go`, `foundation/persistence/conformance/claim_handles_update_scope.go` (rename); `foundation/persistence/conformance/conformance.go` (update subtest registrations to use renamed functions)

**Steps:**

1. Rename file `foundation/persistence/conformance/scope.go` → `claim_scope.go`. Rename the test function inside (e.g., `testScopeByteEquality` → `testClaimScopeByteEquality`).

2. Rename file `foundation/persistence/conformance/claim_handles_update_scope.go` → `claim_handles_update_claim_scope.go`. Rename the test function (`testClaimHandlesUpdateScope` → `testClaimHandlesUpdateClaimScope`).

3. In `foundation/persistence/conformance/conformance.go::Suite`, update the `t.Run(...)` calls to reference the renamed test functions and rename the subtest labels.

**Verification:**
```
test -f foundation/persistence/conformance/claim_scope.go
test -f foundation/persistence/conformance/claim_handles_update_claim_scope.go
test ! -f foundation/persistence/conformance/scope.go
test ! -f foundation/persistence/conformance/claim_handles_update_scope.go
go test ./foundation/persistence/postgres/... -run 'TestSuite/ClaimScope|TestSuite/ClaimHandlesUpdateClaimScope' -count=1
```

---

## Task 82 — Proto rename: `scope` fields in `claim_producer.proto`

**Files:** `protocols/proto/v1/claim_producer.proto` (modify); regenerate via `make proto-gen`

**Steps:**

1. Read `protocols/proto/v1/claim_producer.proto`. The spec calls out: `bytes scope = 3;` on the Acquired message; `bytes scope = 2;` on three other messages; `bytes scope_data = 1;` on yet another; `bytes scope_a = 1;` and `bytes scope_b = 2;` for `ScopesConflictRequest`.

2. Rename each `scope` → `claim_scope` (and `scope_data` → `claim_scope_data`, `scope_a` → `claim_scope_a`, `scope_b` → `claim_scope_b`, `ScopesConflictRequest` → `ClaimScopesConflictRequest`) per the closed-set ClaimScope discipline.

3. Run `make proto-gen` to regenerate Go bindings.

4. Update Go callers of the renamed proto fields.

**Verification:**
```
make proto-gen
go build ./...
```

---

## Task 83 — Substitution directive rename

**Files:** `graph/attribute/substitution.go` (modify); substitution tests and docs

**Steps:**

1. Find the substitution directive handler that parses `{{claim.<alias>.scope}}`. Rename the recognized directive path segment to `claim_scope`.

2. Update tests in `graph/attribute/substitution_test.go`.

3. Update any docs that reference the directive (likely under `docs/` or in concept docs).

**Verification:**
```
go build ./graph/
go test ./graph/attribute/ -count=1
grep -rn "claim\\.<alias>\\.scope" .  # should return nothing in non-historical files
```

---

## Task 84 — CHANGELOG entry

**Files:** `CHANGELOG.md` (modify)

**Steps:**

1. Open `CHANGELOG.md`. Find `## Unreleased`.

2. Append a comprehensive entry describing this spec's changes:

   ```markdown
   - **RunScope as first-class data model.** Introduced `concept:run-scope` (new `rimsky_run_scopes` table) to host execution contexts (main / subgraph / fanout_partition) uniformly. `rimsky_node_runs` loses inline `parent_run_id` + `child_key`; gains non-null `run_scope_id` FK. The two partial-unique in-flight indexes collapse to one keyed on `(node_id, run_scope_id)`. Eliminates the bug class of inline-disambiguator drift at the data-model level. New `AffirmNodeRunRow` lazy-allocation primitive (no return value beyond error; preserves lazy↔eager rewrite property). Cascade walker carries scope through edges; `MarkStaleForCascade` simplifies to pure UPDATE. New `RunScopeTable` persistence accessor; `RunTreeTable` reshape (RunTreeRow.RunScopeID replaces ParentRunID + ChildKey; ListChildren walks via child RunScopes; GetByParentChildKey removed). Pre-v1 break-freely covers migration. See `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`.
   - **Scope → ClaimScope rename.** The existing `concept:scope` (claim-identity bytes per `ClaimProducer.Open`) is renamed `concept:claim-scope` to disambiguate from `concept:run-scope`. Touches: column `rimsky_claim_handles.scope_data` → `claim_scope_data`; CHECK enum `lock_kind = 'scope'` → `'claim_scope'`; index `idx_rimsky_claim_handles_scope` → `..._claim_scope`; Go `ScopesByteEqual` → `ClaimScopesByteEqual`; `ClaimResult.Scope` → `ClaimResult.ClaimScope`; proto `scope` / `scope_data` / `ScopesConflictRequest` field+message renames in `claim_producer.proto`; substitution directive `{{claim.<alias>.scope}}` → `{{claim.<alias>.claim_scope}}`. Pre-v1 break-freely; rebuild executors.
   - **Callback determinism rule.** A callback for a run is honored iff the run's `phase ∈ {active, held}` at acceptance, checked atomically inside the same tx as the state mutation. Otherwise: HTTP 200 ack-but-noop with structured log + `ack_status` in response body. Late callbacks (heartbeat-stale recovery, operator cancel, parked-timeout, duplicate-callback retry) are dropped rather than racing to overwrite canonical state.
   - **Recovery-aware executor protocol.** `ExecuteRequest` gains optional `prior_dispatch_id` + `prior_dispatch_disposition` fields populated when a dispatch supersedes a prior one (heartbeat-stale recovery, retry-after-error, recalculate). HTTP callback ack body gains `ack_status` + `current_dispatch_id`. Executors maintaining per-dispatch session state (e.g., agentic) can build their own handoff semantics on top.
   - **ParkReason proto collapse (7 → 2).** `PARK_REASON_AWAIT_CALLBACK` + `PARK_REASON_SNOOZE`. Removed: `UNSPECIFIED`, `TIME_WAIT`, `SIGNAL_WAIT`, `AWAITING_HUMAN`, `RETRY_BACKOFF`, `CALLBACK_WAIT`, `OTHER`. Schema CHECK constraint enforces. Wire decode rejects invalid values. Pre-v1 break-freely; rebuild executors.
   - **State-machine tx atomicity invariant.** New `@blessed-invariant`: state-machine writes for a single run must be tx-atomic (read-and-write in same tx). Sites fixed: `runtime/on_error.go::OnError` retry + give_up branches (CROSS-TX-SPLIT + nested-tx-deadlock); `runtime/cascade_invalidate.go::invalidateInFrame` (frame_id resolved outside mutating tx — restructured to read inside tx with stale-detection abort).
   - **One-off fixes.** `NodeAttributes.GetLatestByNode` accepts `run_scope_id`. `Nodes.ResetFailedTerminalLastOutcome` accepts `run_scope_id` and skips `rimsky_nodes.updated_at` bump on no-op (driver drift). `IncrementAttributeOverrideMatchCounts` docstring reconciled with actual silent-no-op behavior. Async-callback path under RunScope-first uses `GetRunByDispatchIDForUpdate` (no silent-default-nil best-effort branch).
   - **Test coverage.** 8 must-pass E2E scenarios (4 fan-out, 4 sub-graph). 5 must-pass conformance tests (RunScope lifecycle, AffirmNodeRunRow, in-flight lookup, state-isolation-by-scope, recovery-aware dispatch). State-machine tx atomicity unit test for OnError. TS executor unit test for recovery-aware fields. Aspirational coverage matrix for remaining terminal-type combinations.
   ```

**Verification:**
```
grep "RunScope as first-class data model" CHANGELOG.md
```

---

## Task 85 — Full build + lint sweep

**Files:** (verification-only)

**Steps:**

1. Run the canonical post-change verification per `code:submodules/rimsky/.claude/rules/rules.md`:

   ```
   go build ./...
   make lint
   ```

2. Address any failures by going back to the relevant task.

**Verification:**
```
go build ./... && make lint
```

---

## Task 86 — Full test sweep (non-scenario)

**Files:** (verification-only)

**Steps:**

1. Run the unit + conformance suite:

   ```
   go test ./... -count=1
   ```

2. Race-clean check on persistence + runtime:

   ```
   go test -race ./foundation/persistence/postgres/... ./foundation/persistence/sqlite/... ./runtime/... -count=3
   ```

3. TS executor:

   ```
   cd executors/claude-agent && npm install && npm test && npm run build
   ```

**Verification:**
```
go test ./... -count=1
go test -race ./foundation/persistence/postgres/... ./foundation/persistence/sqlite/... ./runtime/... -count=3
cd executors/claude-agent && npm install && npm test && npm run build
```

---

## Task 87 — Must-pass scenario sweep

**Files:** (verification-only)

**Steps:**

1. Run the 8 must-pass E2E scenarios from Tasks 57–61:

   ```
   go test ./test/scenarios/ -run 'TestFanOut(SuccessCascade|ChildErrorRetry|HeartbeatStaleRecovery|CallbackDeterminism)E2E|TestSubgraph(InternalCascade|ExitCarry|InternalErrorRetry|CascadeThroughExit)E2E' -count=1
   ```

**Verification:**
```
go test ./test/scenarios/ -run 'TestFanOut(SuccessCascade|ChildErrorRetry|HeartbeatStaleRecovery|CallbackDeterminism)E2E|TestSubgraph(InternalCascade|ExitCarry|InternalErrorRetry|CascadeThroughExit)E2E' -count=1
```

---

## Task 88 — Audit re-run (seven patterns)

**Files:** (verification-only)

**Steps:**

1. Run the seven pattern audits per spec §Verification §8:

   - **Audit A:** `grep -rn "WHERE node_id = " foundation/persistence/postgres/ foundation/persistence/sqlite/` — expect zero remaining without an `AND run_scope_id =` clause.
   - **Audit B:** `grep -rn "persistence\\.DispatchRequest{" .` — expect every construction site to include `RunScopeID:` field.
   - **Audit C:** disambiguator USE-site audit — manual walk through every caller of the reshaped persistence methods to confirm `runScopeID` is passed correctly (not nil, not a stale snapshot).
   - **Audit D:** state-machine write atomicity audit — confirm every state-machine write site bundled with its read in a single tx; check for `@blessed-invariant` annotations.
   - **Audit E:** fan-out + sub-graph test coverage gap audit — verify the 8 must-pass scenarios pass.
   - **Audit F:** cascade / wait-set / park-wake under fan-out audit — confirm no stale-snapshot disambiguators in cascade walkers; no hardcoded-nil disambiguators in `walkCascadeForInvalidatedNode` and similar.
   - **Audit G:** ClaimScope rename audit — comprehensive grep for any remaining claim-bytes-Scope identifiers. Run:
     ```
     grep -rn "scope_data\|ScopesByteEqual\|lock_kind = 'scope'\|idx_rimsky_claim_handles_scope\|{{claim\..*\.scope}}\|concept:scope\|TakeScopeLockInTx\|ListByProducerScope\|evaluateScopeConflict\|ParentScope\b\|matchesScope\|SubScopeDescriptor\|SplitScopeRequest\|SplitScopeResponse\|SplitScopeFunc\|\.UpdateScope\b" . \
       --exclude-dir=.ok-planner/history --exclude-dir=.git --exclude-dir=vendor
     ```
     Expect zero remaining matches in active code. Historical references in `.ok-planner/history/` and `tensions/_resolved/` are excluded. The grep covers: the spec's core six (scope_data, ScopesByteEqual, lock_kind enum, index name, substitution directive, concept slug) PLUS Task 81.10's additions (TakeScopeLockInTx, ListByProducerScope, evaluateScopeConflict, ParentScope, matchesScope) PLUS Task 81.8's additions (SubScopeDescriptor, SplitScope*) PLUS Task 81.5's UpdateScope.

2. If any audit returns findings, address them before declaring the plan complete.

**Verification:**
```
# Run each of the seven greps; expect all clean (zero matches or only historical/comment matches).
```

---

## Task 89 — Verify CHANGELOG + concepts.md TOC consistency

**Files:** (verification-only)

**Steps:**

1. Confirm CHANGELOG entry from Task 84 is present and references the spec.

2. Confirm `concepts.md` TOC from Task 79 includes `run-scope` and `claim-scope` (and bare `scope` is gone).

**Verification:**
```
grep "RunScope as first-class data model" CHANGELOG.md
grep -E '^- `run-scope` ' .ok-planner/design/concepts.md
grep -E '^- `claim-scope` ' .ok-planner/design/concepts.md
```

---

## Manual checks after completion

None. Every verification in this plan is automatable via `go build`, `go test`, `make lint`, `grep`, or `test -f` commands. The spec is rimsky-internal — no UI to inspect, no externally-visible behavior beyond the documented proto changes (executor protocol additions, ParkReason collapse), the HTTP callback response body addition, and the schema renames. All covered by scenario + conformance tests.
