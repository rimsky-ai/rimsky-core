# Per-run attribute keying — Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md`
**Goal:** Re-key `rimsky_node_attributes` from per-node to per-run; replace the substitution-context builder with a this-frame-only lookup against drained wait-set rows; add an opt-in `hard_dep: true` flag with cascade-walker proactive invalidation; admit a `{{<directive> | <literal>}}` fallback operator; update five concept docs (including a durable Non-goals subsection on `concept:attribute`).
**Architecture:** Pre-v1 destructive change. New migrations drop and recreate `rimsky_node_attributes` keyed on `node_run_id` with a denormalized `node_id` for forensic queries; the wait-set drain marks rows with a new `drained_at` column rather than deleting them; the substitution context at dispatch reads drained wait-set rows (attribute-topic, settled-success senders) and **nothing else** — no scope-walk, no cross-frame caching; the cascade walker consults a new `BuildHardDepEdges` map to proactively invalidate upstreams for `hard_dep: true` reads.
**Tech Stack:** Go (stdlib + `github.com/jackc/pgx/v5` + `modernc.org/sqlite`), Markdown (concept doc updates), Protobuf (field removal with `reserved` directives), TypeScript (claude-agent executor cleanup).

---

## Context for the implementer

You're picking up a finished design. The spec at `.ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md` is the source of truth for **what to build and why**. This plan translates it into mechanical tasks; read the spec before starting if anything in the plan is ambiguous.

The change is structural and large. **Build order matters** — migrations land before persistence-impl so the SQL schema exists when Go code references new columns; persistence-impl lands before runtime callers; proto changes happen mid-stream because they affect both Go and TypeScript code. The plan orders tasks so that each task's verification can pass at the moment the task completes, except for the explicitly-coupled Tasks 19–21 (HTTP callback adapter triplet) which acknowledges a shared build-gate at Task 21.

**Working directory:** `/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky/` (the rimsky submodule root). All paths in this plan are relative to that root unless otherwise specified.

**Pre-v1 — break freely.** Per `.claude/rules/rules.md`, destructive migrations, proto field removals, and URL route changes are permitted. No backwards-compatibility shims.

**Mandatory verifications per project rules:**
- Any Go change: `go build ./... && go test ./... && make lint`.
- Proto changes: `make proto-gen` first, then Go checks.
- Scenario or storage changes: `go test ./test/scenarios/... ./foundation/persistence/... -count=1` (testcontainers — Docker required).
- Race-sensitive paths: add `-race` to the runtime / persistence / scheduler test runs.
- TypeScript executor: `cd executors/claude-agent && npm install && npm test && npm run build`.

These checks land at the appropriate task boundaries.

**Cold-read conventions** (`.claude/rules/cold-read-cheatsheet.md`): one feature per file, max 2 directory nesting, tests co-located, ~500-line file / ~100-line function guidelines, explicit parameters over DI, logging via stdlib `log/slog` only.

---

## Task 1 — Postgres migration: re-key `rimsky_node_attributes` to per-run

**Files:** `foundation/persistence/postgres/migrations/003-per-run-attributes.sql` (new).

### Step 1.1 — Inspect existing migration shape

Read `foundation/persistence/postgres/migrations/001-baseline.sql` lines 293–302 (the existing `rimsky_node_attributes` definition) and `foundation/persistence/postgres/migrations/002-tags.sql`. The convention is zero-padded number prefix + descriptive slug + `.sql`; migrations are append-only.

### Step 1.2 — Write the new migration file

Create `foundation/persistence/postgres/migrations/003-per-run-attributes.sql`:

```sql
-- =====  rimsky_node_attributes  (re-keyed per-run)  =====
-- Pre-v1 destructive rekeying: drop the existing per-node table and
-- recreate keyed by node_run_id, with node_id denormalized for
-- forensic queries.  Per spec
-- .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
-- §"Data model — re-key rimsky_node_attributes".
DROP TABLE IF EXISTS rimsky_node_attributes;

CREATE TABLE rimsky_node_attributes (
    node_run_id          UUID PRIMARY KEY REFERENCES rimsky_node_runs(id) ON DELETE CASCADE,
    node_id              UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    data                 JSONB NOT NULL DEFAULT '{}'::jsonb,
    value_handle         TEXT,
    value_handle_backend TEXT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX rimsky_node_attributes_node_idx
    ON rimsky_node_attributes (node_id, updated_at DESC);
```

The `run_attempt INT` column from the old shape is intentionally absent.

### Step 1.3 — Verify

```
go test ./foundation/persistence/postgres/... -count=1
```

Expect: Go compile errors in downstream callers are likely at this stage and acceptable. The migration SQL itself must be valid (no SQL syntax errors from `migrator.Up()`).

---

## Task 2 — SQLite migration: same re-key

**Files:** `foundation/persistence/sqlite/migrations/003-per-run-attributes.sql` (new).

### Step 2.1 — Write the SQLite migration

```sql
DROP TABLE IF EXISTS rimsky_node_attributes;

CREATE TABLE rimsky_node_attributes (
    node_run_id          TEXT PRIMARY KEY REFERENCES rimsky_node_runs(id) ON DELETE CASCADE,
    node_id              TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    data                 TEXT NOT NULL DEFAULT '{}',
    value_handle         TEXT,
    value_handle_backend TEXT,
    updated_at           TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX rimsky_node_attributes_node_idx
    ON rimsky_node_attributes (node_id, updated_at DESC);
```

Verify column-type conventions against `foundation/persistence/sqlite/migrations/001-baseline.sql` lines 257–272 (TEXT for UUIDs, TEXT for JSON, `CURRENT_TIMESTAMP` default).

### Step 2.2 — Verify

```
go test ./foundation/persistence/sqlite/... -count=1
```

---

## Task 3 — Postgres migration: `drained_at` column on `rimsky_wait_set`

**Files:** `foundation/persistence/postgres/migrations/004-wait-set-drained-at.sql` (new).

### Step 3.1 — Write the migration

```sql
-- =====  rimsky_wait_set  (mark-don't-delete on drain)  =====
-- Pre-v1 additive: drain marks the row's drained_at timestamp
-- instead of deleting the row.  Eligibility predicate updates to
-- "no rows with drained_at IS NULL." Drained rows are queryable by
-- the substitution-context builder.  Per spec
-- .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
-- §"Wait-set — mark-don't-delete on drain".
ALTER TABLE rimsky_wait_set ADD COLUMN drained_at TIMESTAMPTZ;
```

No new index — the existing receiver/sender indexes are sufficient.

### Step 3.2 — Verify

```
go test ./foundation/persistence/postgres/... -count=1
```

---

## Task 4 — SQLite migration: same drained_at column

**Files:** `foundation/persistence/sqlite/migrations/004-wait-set-drained-at.sql` (new).

### Step 4.1 — Write the migration

```sql
ALTER TABLE rimsky_wait_set ADD COLUMN drained_at TEXT;
```

SQLite stores timestamps as TEXT (ISO-8601 strings).

### Step 4.2 — Verify

```
go test ./foundation/persistence/sqlite/... -count=1
```

---

## Task 5 — Update `NodeAttributesRow` struct and `NodeAttributeTable` interface

**Files:** `foundation/persistence/node_attributes.go`.

### Step 5.1 — Read the current shape

The current shape (`code:foundation/persistence/node_attributes.go`):

```go
type NodeAttributesRow struct {
    NodeID     shared.UUID
    RunAttempt int
    Data       map[string]any
    UpdatedAt  time.Time
}

type NodeAttributeTable interface {
    Get(ctx context.Context, nodeID shared.UUID, tx Tx) (*NodeAttributesRow, error)
    Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any, tx Tx) error
    MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any, tx Tx) error
}
```

### Step 5.2 — Replace with the per-run shape

```go
// NodeAttributesRow mirrors a row of rimsky_node_attributes (post-2026-05-20
// per-run keying).
type NodeAttributesRow struct {
    NodeRunID shared.UUID
    NodeID    shared.UUID  // denormalized for forensic queries
    Data      map[string]any
    UpdatedAt time.Time
}

// NodeAttributeTable is the rimsky_node_attributes accessor.
//
// GetByRun returns (nil, nil) when the row is absent — absence is a
// normal lifecycle state (the row is created lazily on first dispatch).
//
// GetLatestByNode returns the most-recent run's attribute row for the
// given node, used by forensic / observability paths (control-api,
// lineage projections, agent dashboards). Returns (nil, nil) when the
// node has no runs.
//
// Upsert replaces `data` outright; MergeDelta performs a SHALLOW JSONB
// merge and requires the row to exist.
type NodeAttributeTable interface {
    GetByRun(ctx context.Context, runID shared.UUID, tx Tx) (*NodeAttributesRow, error)
    GetLatestByNode(ctx context.Context, nodeID shared.UUID, tx Tx) (*NodeAttributesRow, error)
    Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any, tx Tx) error
    MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any, tx Tx) error
}
```

### Step 5.3 — Verify

```
go build ./foundation/persistence/
```

The bare `persistence` package builds cleanly. Implementation packages (postgres / sqlite) fail until Tasks 6/7 land — expected.

---

## Task 6 — Update postgres `NodeAttributeTable` implementation (preserving blob-spill)

**Files:** `foundation/persistence/postgres/node_attributes.go`.

### Step 6.1 — Read the current implementation in full

Read all of `foundation/persistence/postgres/node_attributes.go` (~305 lines). The file is **substantially larger than a thin SQL wrapper** because of blob-spill machinery:

- **`Get`** dereferences `value_handle` / `value_handle_backend` through the configured `persistence.BlobBackend` when the row is spilled. Cross-backend topology mismatches fall back to the inline `data` column. Missing-blob (e.g., `SweepOrphanedBlobs` race) returns an empty data map rather than erroring.
- **`Upsert`** marshals `data`, calls `persistence.ShouldSpillBlob(si.blob, si.blobThreshold, len(raw))` to decide whether to spill, writes through `si.blob.Write(ctx, BlobKey{...}, raw)` when spilling (storing the returned handle in `value_handle` and clearing `data` to `'{}'::jsonb`), and queues the prior handle for orphan reaping via `persistence.QueueBlobOrphan` when overwriting.
- **`MergeDelta`** has two paths: for spilled rows, materialize the existing bytes via `Get`, merge in Go, re-`Upsert` (which re-applies the spill decision). For inline rows, run a SQL-level `data || $2::jsonb` merge.
- **`readPriorBlobHandle`** is a helper that reads `value_handle` / `value_handle_backend` for the current row.

**Preserve all of this.** The per-run keying rewrite is keyed by `node_run_id` instead of `node_id`, but the blob-spill machinery stays intact. The implementer must NOT reduce the file to a thin SQL wrapper.

### Step 6.2 — Conventions before editing

- Postgres tables use `b.q(tx)` (the `q` method on `tablesImpl`) to get a pgx querier — not `b.q(tx)`. See `code:foundation/persistence/postgres/backend.go::tablesImpl.q` and the consistent usage across all sibling table accessors.
- The accessor struct is `nodeAttributesImpl`; the helper `(*tablesImpl)(s)` cast exists to reach the backing tablesImpl fields (`blob`, `blobThreshold`, `blobRetention`, `BlobOrphans()`).

### Step 6.3 — Rewrite `Get` → `GetByRun(ctx, runID, tx)`

Re-key the WHERE clause from `node_id = $1` to `node_run_id = $1`. Update the SELECT column list to include both `node_run_id` and `node_id`. Update the Scan to populate `out.NodeRunID` and `out.NodeID`. Drop the `run_attempt` scan target. Preserve every line of the blob-dereference logic.

```go
func (s *nodeAttributesImpl) GetByRun(ctx context.Context, runID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
    row := s.q(tx).QueryRow(ctx,
        `SELECT node_run_id, node_id, data, updated_at, value_handle, value_handle_backend
           FROM rimsky_node_attributes
          WHERE node_run_id = $1`, runID,
    )
    var (
        out         persistence.NodeAttributesRow
        raw         []byte
        when        time.Time
        handle      *string
        handleBkend *string
    )
    if err := row.Scan(&out.NodeRunID, &out.NodeID, &raw, &when, &handle, &handleBkend); err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, nil
        }
        return nil, fmt.Errorf("node_attributes.GetByRun: %w", err)
    }
    out.UpdatedAt = when
    // Spill-read: identical machinery to today's Get (blob backend
    // dereference, cross-backend mismatch fallback, missing-blob → empty
    // data map). Copy verbatim from the current implementation; only
    // the WHERE clause and scan targets changed.
    bb := (*tablesImpl)(s).blob
    if handle != nil && *handle != "" && bb != nil && handleBkend != nil && *handleBkend == bb.Name() {
        bytes, err := bb.Read(ctx, persistence.Handle(*handle))
        if err != nil {
            if errors.Is(err, persistence.ErrBlobNotFound) {
                out.Data = map[string]any{}
                return &out, nil
            }
            return nil, fmt.Errorf("node_attributes.GetByRun: blob.Read(%s): %w", *handle, err)
        }
        m := map[string]any{}
        if len(bytes) > 0 {
            if err := json.Unmarshal(bytes, &m); err != nil {
                return nil, fmt.Errorf("node_attributes.GetByRun: unmarshal blob bytes: %w", err)
            }
        }
        out.Data = m
        return &out, nil
    }
    if len(raw) == 0 {
        out.Data = map[string]any{}
    } else {
        m := map[string]any{}
        if err := json.Unmarshal(raw, &m); err != nil {
            return nil, fmt.Errorf("node_attributes.GetByRun: unmarshal data: %w", err)
        }
        out.Data = m
    }
    return &out, nil
}
```

### Step 6.4 — Add `GetLatestByNode(ctx, nodeID, tx)`

New method. Used for forensic / observability paths (control-api, lineage projections, agent dashboards). Same blob-dereference logic as `GetByRun`; SQL differs in WHERE + ORDER BY + LIMIT.

```go
func (s *nodeAttributesImpl) GetLatestByNode(ctx context.Context, nodeID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
    row := s.q(tx).QueryRow(ctx,
        `SELECT node_run_id, node_id, data, updated_at, value_handle, value_handle_backend
           FROM rimsky_node_attributes
          WHERE node_id = $1
          ORDER BY updated_at DESC
          LIMIT 1`, nodeID,
    )
    // Scan + spill-deref logic identical to GetByRun; factor a private
    // helper `scanAndDeref(row, ctx)` if duplication feels heavy.
    // ...
}
```

The `(node_id, updated_at DESC)` index (Task 1) makes this a single index seek.

### Step 6.5 — Rewrite `Upsert` to `(ctx, runID, nodeID, data, tx)` preserving blob-spill

Key changes:
- Signature drops `runAttempt int`, adds `runID shared.UUID` + `nodeID shared.UUID`.
- INSERT column list: `node_run_id, node_id, data, updated_at, value_handle, value_handle_backend`.
- ON CONFLICT clause: `ON CONFLICT (node_run_id) DO UPDATE` (was `ON CONFLICT (node_id)`).
- `EXCLUDED.run_attempt` removed from the SET clause (column dropped).
- `BlobKey.NodeID` field: pass `runID.String()` (per the BlobKey docstring, "empty strings are valid for callers that do not have node or attribute context (e.g. parked-payload writes are keyed by node-run id rather than node id)" — under per-run keying, the run-id is the canonical identifier and aligns blob keys with the new shape). `BlobKey.AttributeName` stays `"data"`.
- `readPriorBlobHandle` is keyed by `node_run_id` (Step 6.7).
- Orphan-queueing logic preserved verbatim — when the prior row had a value_handle that's about to be replaced, queue it for `SweepOrphanedBlobs`.

```go
func (s *nodeAttributesImpl) Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any, tx persistence.Tx) error {
    if data == nil {
        data = map[string]any{}
    }
    raw, err := json.Marshal(data)
    if err != nil {
        return fmt.Errorf("node_attributes.Upsert: marshal: %w", err)
    }

    si := (*tablesImpl)(s)
    priorHandle, priorBkend, err := readPriorBlobHandle(ctx, si.q(tx), runID)
    if err != nil {
        return fmt.Errorf("node_attributes.Upsert: read prior handle: %w", err)
    }

    var (
        newHandle  string
        newBackend string
        dataToSave = raw
    )
    if persistence.ShouldSpillBlob(si.blob, si.blobThreshold, len(raw)) {
        h, werr := si.blob.Write(ctx, persistence.BlobKey{
            NodeID:        runID.String(), // per-run keying
            AttributeName: "data",
        }, raw)
        if werr != nil {
            return fmt.Errorf("node_attributes.Upsert: blob.Write: %w", werr)
        }
        newHandle = string(h)
        newBackend = si.blob.Name()
        dataToSave = []byte(`{}`)
    }

    if newHandle != "" {
        _, err = si.q(tx).Exec(ctx,
            `INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, updated_at, value_handle, value_handle_backend)
             VALUES ($1, $2, $3::jsonb, now(), $4, $5)
             ON CONFLICT (node_run_id) DO UPDATE
               SET data                 = EXCLUDED.data,
                   value_handle         = EXCLUDED.value_handle,
                   value_handle_backend = EXCLUDED.value_handle_backend,
                   updated_at           = now()`,
            runID, nodeID, dataToSave, newHandle, newBackend,
        )
    } else {
        // Inline path — explicitly clear any prior value_handle when
        // downgrading from spilled to inline.
        _, err = si.q(tx).Exec(ctx,
            `INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, updated_at, value_handle, value_handle_backend)
             VALUES ($1, $2, $3::jsonb, now(), NULL, NULL)
             ON CONFLICT (node_run_id) DO UPDATE
               SET data                 = EXCLUDED.data,
                   value_handle         = NULL,
                   value_handle_backend = NULL,
                   updated_at           = now()`,
            runID, nodeID, dataToSave,
        )
    }
    if err != nil {
        return fmt.Errorf("node_attributes.Upsert: %w", err)
    }

    if priorHandle != "" && priorHandle != newHandle {
        now := time.Now().UTC()
        if err := persistence.QueueBlobOrphan(ctx, si.BlobOrphans(), tx,
            priorHandle, priorBkend, now, si.blobRetention); err != nil {
            return fmt.Errorf("node_attributes.Upsert: queue prior orphan: %w", err)
        }
    }
    return nil
}
```

### Step 6.6 — Rewrite `MergeDelta` to `(ctx, runID, delta, tx)` preserving spilled-row path

Key changes:
- Signature drops `nodeID`, takes `runID`.
- WHERE clause: `node_run_id = $1`.
- Spilled-row path: call `s.GetByRun(ctx, runID, tx)` to materialize, merge in Go, re-`Upsert(ctx, runID, prior.NodeID, merged, tx)`.
- Inline path: SQL `data || $2::jsonb` merge keyed by `node_run_id`.
- `nil`-delta no-op: `UPDATE ... SET updated_at = now() WHERE node_run_id = $1`.
- `ErrNotFound` wrapping preserved on absent-row delta.

```go
func (s *nodeAttributesImpl) MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any, tx persistence.Tx) error {
    if delta == nil {
        _, err := s.q(tx).Exec(ctx,
            `UPDATE rimsky_node_attributes SET updated_at = now() WHERE node_run_id = $1`, runID,
        )
        if err != nil {
            return fmt.Errorf("node_attributes.MergeDelta: touch: %w", err)
        }
        return nil
    }

    si := (*tablesImpl)(s)
    priorHandle, _, err := readPriorBlobHandle(ctx, si.q(tx), runID)
    if err != nil {
        return fmt.Errorf("node_attributes.MergeDelta: read prior handle: %w", err)
    }
    if priorHandle != "" {
        prior, err := s.GetByRun(ctx, runID, tx)
        if err != nil {
            return fmt.Errorf("node_attributes.MergeDelta: get prior: %w", err)
        }
        if prior == nil {
            return fmt.Errorf("node_attributes.MergeDelta: %w", persistence.ErrNotFound)
        }
        merged := prior.Data
        if merged == nil {
            merged = map[string]any{}
        }
        for k, v := range delta {
            merged[k] = v
        }
        return s.Upsert(ctx, runID, prior.NodeID, merged, tx)
    }

    raw, err := json.Marshal(delta)
    if err != nil {
        return fmt.Errorf("node_attributes.MergeDelta: marshal: %w", err)
    }
    tag, err := s.q(tx).Exec(ctx,
        `UPDATE rimsky_node_attributes
            SET data = data || $2::jsonb, updated_at = now()
          WHERE node_run_id = $1`, runID, raw,
    )
    if err != nil {
        return fmt.Errorf("node_attributes.MergeDelta: %w", err)
    }
    if tag.RowsAffected() == 0 {
        return fmt.Errorf("node_attributes.MergeDelta: %w", persistence.ErrNotFound)
    }
    return nil
}
```

### Step 6.7 — Update `readPriorBlobHandle` helper

Signature changes from `(ctx, q querier, nodeID)` to `(ctx, q querier, runID)`. WHERE clause from `node_id = $1` to `node_run_id = $1`. Logic otherwise unchanged.

### Step 6.8 — Verify

```
go build ./foundation/persistence/postgres/
go test ./foundation/persistence/postgres/ -count=1 -run TestNodeAttributes
```

Targeted tests must pass. The full persistence-suite tests may still fail until the conformance suite (Task 11) lands.

### Step 6.9 — Sanity check: blob-spill machinery preserved

```
grep -c 'BlobBackend\|ShouldSpillBlob\|QueueBlobOrphan\|readPriorBlobHandle\|BlobKey' foundation/persistence/postgres/node_attributes.go
```

Expect at least 5. If 0, the implementer has accidentally stripped the spill path and the file needs to be reworked before proceeding.

---

## Task 7 — Update sqlite `NodeAttributeTable` implementation (preserving blob-spill)

**Files:** `foundation/persistence/sqlite/node_attributes.go`.

### Step 7.1 — Read the current SQLite implementation in full

Read all of `foundation/persistence/sqlite/node_attributes.go` (~294 lines). Like the postgres version, it has full blob-spill machinery (Get blob-dereference, Upsert spill decision + handle storage + orphan queueing, MergeDelta spilled-row materialize-then-re-Upsert, `readPriorBlobHandle` helper). **Preserve all of it.**

### Step 7.2 — Mirror Task 6's per-run rewrite

Apply the same logical changes as Task 6:
- `Get` → `GetByRun(ctx, runID, tx)`, scan adds `node_run_id`, drops `run_attempt`.
- New `GetLatestByNode(ctx, nodeID, tx)`.
- `Upsert(ctx, runID, nodeID, data, tx)` with spill decision + handle storage + orphan queueing preserved.
- `MergeDelta(ctx, runID, delta, tx)` with the spilled-row materialize-then-re-Upsert path preserved.
- `readPriorBlobHandle` keyed by `node_run_id`.

SQLite-specific differences from Task 6's postgres pseudocode:
- `?` placeholders instead of `$1`/`$2`.
- Timestamp default is `datetime('now')` (verify against existing convention).
- The MergeDelta inline-path uses `json_patch(data, ?)` (JSON1 extension; verify the existing impl uses this).
- The querier convention is the SQLite equivalent of the postgres `b.q(tx)` — verify the existing sibling tables (e.g., `code:foundation/persistence/sqlite/wait_set.go`) for the exact accessor pattern before editing.

The PK `ON CONFLICT (node_run_id) DO UPDATE` clause is supported by SQLite as `ON CONFLICT(node_run_id) DO UPDATE`.

### Step 7.3 — Verify

```
go build ./foundation/persistence/sqlite/
go test ./foundation/persistence/sqlite/ -count=1 -run TestNodeAttributes
```

### Step 7.4 — Sanity check: blob-spill machinery preserved

```
grep -c 'BlobBackend\|ShouldSpillBlob\|QueueBlobOrphan\|readPriorBlobHandle\|BlobKey' foundation/persistence/sqlite/node_attributes.go
```

Expect at least 5.

---

## Task 8 — Update `WaitSetRow` struct, rename drain primitive, add list method, fix stale topic comment

**Files:** `foundation/persistence/wait_set.go`.

### Step 8.1 — Read current interface

Read `foundation/persistence/wait_set.go` in full.

### Step 8.2 — Add `DrainedAt` to `WaitSetRow`

Add a nullable timestamp field:

```go
type WaitSetRow struct {
    FrameID           shared.UUID
    ReceiverRunID     shared.UUID
    SenderRunID       shared.UUID
    TopicKind         string
    SubscriptionScope string
    TopicFilter       json.RawMessage
    DrainedAt         *time.Time  // nil means not yet drained
}
```

Add `"time"` import if not already present.

### Step 8.3 — Rename `DeleteBySender` → `MarkDrainedBySender`

In the interface, rename the method and update its docstring:

```go
// MarkDrainedBySender bulk-marks every wait-set row where
// (frame_id, sender_run_id) match as drained (sets drained_at to NOW()).
// Drained rows remain queryable for the substitution-context builder.
// Idempotent: rows already drained are not re-touched. Replaces the
// prior DeleteBySender semantic (rows used to be deleted on drain;
// post-2026-05-20 they're retained for trigger-context queries).
MarkDrainedBySender(ctx context.Context, frameID, senderRunID shared.UUID, tx Tx) error
```

### Step 8.4 — Add `ListDrainedAttributeRowsForReceiver`

```go
// ListDrainedAttributeRowsForReceiver returns the drained wait-set
// rows for the receiver in the frame, filtered to topic_kind='attribute'.
// Used by the substitution-context builder to enumerate sender_run_ids
// that contributed to this dispatch via attribute-topic edges.
//
// Per .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
// §"Substitution context builder".
ListDrainedAttributeRowsForReceiver(
    ctx context.Context, frameID, receiverRunID shared.UUID, tx Tx,
) ([]WaitSetRow, error)
```

### Step 8.5 — Verify

```
go build ./foundation/persistence/
```

---

## Task 9 — Update postgres `WaitSetTable` implementation

**Files:** `foundation/persistence/postgres/wait_set.go`.

### Step 9.1 — Replace `DeleteBySender` with `MarkDrainedBySender`

```go
func (b *waitSetImpl) MarkDrainedBySender(ctx context.Context, frameID, senderRunID shared.UUID, tx persistence.Tx) error {
    sql := `UPDATE rimsky_wait_set
            SET drained_at = NOW()
            WHERE frame_id = $1 AND sender_run_id = $2 AND drained_at IS NULL`
    _, err := b.q(tx).Exec(ctx, sql, frameID, senderRunID)
    return err
}
```

The `AND drained_at IS NULL` guard makes the operation idempotent.

### Step 9.2 — Add `ListDrainedAttributeRowsForReceiver`

```go
func (b *waitSetImpl) ListDrainedAttributeRowsForReceiver(
    ctx context.Context, frameID, receiverRunID shared.UUID, tx persistence.Tx,
) ([]persistence.WaitSetRow, error) {
    sql := `SELECT frame_id, receiver_run_id, sender_run_id, topic_kind,
                   subscription_scope, topic_filter, drained_at
            FROM rimsky_wait_set
            WHERE frame_id = $1 AND receiver_run_id = $2
              AND drained_at IS NOT NULL
              AND topic_kind = 'attribute'`
    rows, err := b.q(tx).Query(ctx, sql, frameID, receiverRunID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []persistence.WaitSetRow
    for rows.Next() {
        var r persistence.WaitSetRow
        var topicFilter []byte
        var drainedAt *time.Time
        if err := rows.Scan(
            &r.FrameID, &r.ReceiverRunID, &r.SenderRunID,
            &r.TopicKind, &r.SubscriptionScope,
            &topicFilter, &drainedAt,
        ); err != nil {
            return nil, err
        }
        if topicFilter != nil {
            r.TopicFilter = topicFilter
        }
        r.DrainedAt = drainedAt
        out = append(out, r)
    }
    return out, rows.Err()
}
```

### Step 9.3 — Update existing list methods to scan `drained_at`

`ListForReceiver` and `ListForFrame` (per the interface) — update their SELECT to include `drained_at` in the column list and their scan to populate `r.DrainedAt`. Behavior otherwise unchanged.

### Step 9.4 — Verify

```
go build ./foundation/persistence/postgres/
go test ./foundation/persistence/postgres/ -count=1 -run TestWaitSet
```

---

## Task 10 — Update sqlite `WaitSetTable` implementation

**Files:** `foundation/persistence/sqlite/wait_set.go`.

### Step 10.1 — Mirror Task 9 in SQLite

SQLite-specific:
- `?` placeholders.
- `datetime('now')` instead of `NOW()`.
- DrainedAt scans from TEXT; parse with `time.Parse(time.RFC3339, ...)` if non-NULL.

### Step 10.2 — Verify

```
go build ./foundation/persistence/sqlite/
go test ./foundation/persistence/sqlite/ -count=1 -run TestWaitSet
```

---

## Task 11 — Update `NodeAttributeTable` conformance test

**Files:** `foundation/persistence/conformance/node_attributes_merge_delta.go` (existing), `foundation/persistence/conformance/node_attributes_per_run.go` (new).

### Step 11.1 — Read the existing conformance file

Read `foundation/persistence/conformance/node_attributes_merge_delta.go` and the dispatch table at `conformance.go` / `conformance_test.go`.

### Step 11.2 — Rewrite existing tests to per-run shape

For every `store.NodeAttributes().Upsert(ctx, fix.NodeID, 0, initial, tx)`, change to `store.NodeAttributes().Upsert(ctx, fix.NodeRunID, fix.NodeID, initial, tx)`.

For every `store.NodeAttributes().Get(ctx, fix.NodeID, tx)`, change to `store.NodeAttributes().GetByRun(ctx, fix.NodeRunID, tx)`.

If the fixture lacks `NodeRunID`, use `seedConformanceRunForNode` at `code:foundation/persistence/conformance/fixtures.go#120` to seed a real `rimsky_node_runs` row. Do not re-invent the enqueue/claim flow.

### Step 11.3 — Add new tests in `node_attributes_per_run.go`

- `NodeAttributesPerRunInsertByRun` — insert by run, get by run, assert data round-trips.
- `NodeAttributesGetLatestByNode` — insert two runs for the same node with different `data`, call `GetLatestByNode`, assert the row with the later `updated_at` returns.
- `NodeAttributesCascadeDeleteWithRun` — insert a run + attribute row, delete the run row via raw SQL (`tx.Exec("DELETE FROM rimsky_node_runs WHERE id = $1", runID)` for postgres or `?` for SQLite), assert the attribute row is also gone. `persistence.RunTreeTable` has no `Delete` method by design; raw SQL is the right approach for this test.
- `NodeAttributesPerRunDenormConsistency` — verify the denormalized `node_id` matches the canonical `rimsky_node_runs.node_id`.

Wire each new function into the conformance dispatch table.

### Step 11.4 — Verify

```
go test ./foundation/persistence/conformance/ -count=1
go test ./foundation/persistence/postgres/ -count=1 -run TestConformance
go test ./foundation/persistence/sqlite/ -count=1 -run TestConformance
```

---

## Task 12 — Update `WaitSetTable` conformance test

**Files:** `foundation/persistence/conformance/wait_set.go`.

### Step 12.1 — Update existing tests for the rename

Read the file. Any call to `DeleteBySender` renames to `MarkDrainedBySender`. Update behavioral expectations: rows should remain after drain, with `DrainedAt != nil`.

### Step 12.2 — Add new tests

- `WaitSetMarkDrainedBySenderRetainsRow` — insert a wait-set row, call `MarkDrainedBySender`, assert the row is still queryable via `ListForReceiver` with `DrainedAt != nil`.
- `WaitSetMarkDrainedBySenderIdempotent` — call twice, assert no error and `drained_at` stays at its initial value.
- `WaitSetListDrainedAttributeRowsForReceiver` — insert several rows with mixed topic_kinds, drain some senders, call the new list method, assert only `drained_at IS NOT NULL AND topic_kind='attribute'` rows return.

Wire into the dispatch table.

### Step 12.3 — Verify

```
go test ./foundation/persistence/conformance/ -count=1
go test ./foundation/persistence/postgres/ -count=1 -run TestConformance
go test ./foundation/persistence/sqlite/ -count=1 -run TestConformance
```

---

## Task 13 — Postgres eligibility predicate SQL update

**Files:** `foundation/persistence/postgres/nodes.go`.

### Step 13.1 — Locate the eligibility predicates

Read `code:foundation/persistence/postgres/nodes.go::ListReadyForDispatch#222` and `::ListPureCascadeReady#249`. Both contain a `NOT EXISTS` subquery against `rimsky_wait_set`.

### Step 13.2 — Update the subquery

Actual current SQL for `ListReadyForDispatch` (`code:foundation/persistence/postgres/nodes.go#229-232`):

```sql
NOT EXISTS (
    SELECT 1 FROM rimsky_wait_set w
    WHERE w.frame_id = n.frame_id AND w.receiver_run_id = r.id
)
```

After:

```sql
NOT EXISTS (
    SELECT 1 FROM rimsky_wait_set w
    WHERE w.frame_id = n.frame_id AND w.receiver_run_id = r.id
      AND w.drained_at IS NULL
)
```

Make the same edit (one new line: `AND w.drained_at IS NULL`) to the `NOT EXISTS` subquery in `ListPureCascadeReady`. Verify alias in that function before editing — likely also `w`.

### Step 13.3 — Verify

```
go build ./foundation/persistence/postgres/
go test ./foundation/persistence/postgres/ -count=1 -run TestListReady
```

---

## Task 14 — SQLite eligibility predicate SQL update

**Files:** `foundation/persistence/sqlite/nodes.go`.

### Step 14.1 — Same predicate update

Locate the SQLite counterparts at `foundation/persistence/sqlite/nodes.go` around line 180-260. The SQLite SQL uses the same `w` alias as postgres; add `AND w.drained_at IS NULL` to both `ListReadyForDispatch` and `ListPureCascadeReady`.

### Step 14.2 — Verify

```
go build ./foundation/persistence/sqlite/
go test ./foundation/persistence/sqlite/ -count=1 -run TestListReady
```

---

## Task 15 — Remove `run_attempt` from `ExecuteRequest` proto + downstream cleanup

**Files:** `protocols/proto/v1/executor.proto`, `protocols/executor/types.go`, `executors/claude-agent/src/http-bridge.ts`, `executors/claude-agent/src/server.ts`, `runtime/runner_dispatch.go`.

### Step 15.1 — Locate and modify proto

Read `protocols/proto/v1/executor.proto`. Find `ExecuteRequest` message, locate field 11 (`run_attempt`). Replace the field declaration with:

```proto
  reserved 11;
  reserved "run_attempt";
```

Match the convention at lines 68-69 of the same file (which has `reserved 10; reserved "resumed";` for the retired `resumed` field).

### Step 15.2 — Regenerate proto

```
make proto-gen
```

### Step 15.3 — Verify Go regeneration drops the field

```
grep -n "RunAttempt" protocols/proto/v1/gen/executor*.pb.go
```

Should return no matches for `RunAttempt` on `ExecuteRequest`.

### Step 15.4 — Fix downstream Go and TypeScript references

Known sites to remove:

- **`code:protocols/executor/types.go#22`** — `RunAttempt int32` field on the hand-written Go shadow of `ExecuteRequest`. Remove.
- **`code:executors/claude-agent/src/http-bridge.ts#84`** — `run_attempt?: number;`. Remove.
- **`code:executors/claude-agent/src/server.ts#100`** — `run_attempt?: number;`. Remove.
- **`runtime/runner_dispatch.go::buildExecuteRequest`** — drop the `RunAttempt: ...` assignment (the read site at #714-724 is addressed by Task 20).

Search for additional sites:

```
grep -rn "ExecuteRequest.*RunAttempt\|\.RunAttempt =\|run_attempt" runtime/ executors/ test/ protocols/
```

Executors that previously read `run_attempt` for idempotency migrate to `proto:executor.proto::ExecuteRequest.dispatch_id` (field 12), already present and documented as "The supervisor-side rimsky_node_runs.id for this dispatch."

### Step 15.5 — Verify Go and TypeScript builds

```
go build ./...
cd executors/claude-agent && npm install && npm test && npm run build && cd ../..
```

---

## Task 16 — Remove `run_attempt` from `AttributesSubstitutedPayload` proto

**Files:** `protocols/proto/v1/events.proto`.

### Step 16.1 — Locate and modify

Find `AttributesSubstitutedPayload`, locate field 3 (`run_attempt`). Replace with:

```proto
  reserved 3;
  reserved "run_attempt";
```

### Step 16.2 — Regenerate

```
make proto-gen
```

### Step 16.3 — Fix downstream Go references

```
grep -rn "AttributesSubstitutedPayload.*RunAttempt" runtime/ control/ test/
```

Remove these references. The event emitter site (search for the AttributesSubstitutedPayload populate) drops the assignment. Consumers reading `run_attempt` for retry-counting migrate to `consecutive_retries_no_progress` on `rimsky_node_runs`.

```
go build ./...
```

---

## Task 17 — Remove `run_attempt` from runtime `upsertAttributesPreDispatch`

**Files:** `runtime/runner.go`.

### Step 17.1 — Read current behavior

`code:runtime/runner.go::upsertAttributesPreDispatch#523`:

```go
func upsertAttributesPreDispatch(
    ctx context.Context, args RunArgs, nodeID shared.UUID, resolvedAttrs map[string]any,
) error {
    return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
        prior, _ := args.Persist.NodeAttributes().Get(ctx, nodeID, tx)
        attempt := 1
        if prior != nil {
            attempt = prior.RunAttempt + 1
        }
        return args.Persist.NodeAttributes().Upsert(ctx, nodeID, attempt, resolvedAttrs, tx)
    })
}
```

### Step 17.2 — Rewrite

```go
func upsertAttributesPreDispatch(
    ctx context.Context, args RunArgs, runID, nodeID shared.UUID, resolvedAttrs map[string]any,
) error {
    return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
        return args.Persist.NodeAttributes().Upsert(ctx, runID, nodeID, resolvedAttrs, tx)
    })
}
```

The prior-read transaction block (lines 529–536) is removed entirely — each run is a fresh row by PK, no read needed.

### Step 17.3 — Update callers

```
grep -rn "upsertAttributesPreDispatch" runtime/
```

Each caller passes `acq.DispatchID` (the runID) and `acq.NodeID`.

### Step 17.4 — Verify

```
go build ./runtime/
```

---

## Task 18 — Remove `run_attempt` from `upsertFinalAttributesTx`

**Files:** `runtime/runner_terminal.go`.

### Step 18.1 — Read current behavior

`code:runtime/runner_terminal.go::upsertFinalAttributesTx#609`.

### Step 18.2 — Rewrite

```go
func upsertFinalAttributesTx(
    ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, merged map[string]any,
) error {
    prior, _ := args.Persist.NodeAttributes().GetByRun(ctx, acq.DispatchID, tx)
    final := merged
    if prior != nil && len(prior.Data) > 0 {
        combined := make(map[string]any, len(prior.Data)+len(merged))
        for k, v := range prior.Data {
            combined[k] = v
        }
        for k, v := range merged {
            combined[k] = v
        }
        final = combined
    }
    if final == nil {
        final = map[string]any{}
    }
    return args.Persist.NodeAttributes().Upsert(ctx, acq.DispatchID, acq.NodeID, final, tx)
}
```

The `attempt` variable disappears; the merge logic stays.

### Step 18.3 — Verify

```
go build ./runtime/
```

---

## Task 19 — Update `attributesStoreAdapter` in `runtime/callback.go`

**Files:** `runtime/callback.go`.

### Step 19.1 — Read current adapter

Read `runtime/callback.go` around lines 485–515. The `attributesStoreAdapter` methods bridge `persistence.NodeAttributeTable` (which takes a `tx`) to the narrower `attributes.NodeAttributeTable` (which doesn't). Current methods:

- `Get(ctx, nodeID)` at #485
- `Upsert(ctx, nodeID, runAttempt, data)` at #505
- `MergeDelta(ctx, nodeID, delta)` at #511

### Step 19.2 — Rewrite to per-run

Update the adapter to take `runID` instead of `nodeID`. The callback handler resolves the cancel_token to a `runID` (per Task 21).

```go
func (a *attributesStoreAdapter) GetByRun(ctx context.Context, runID shared.UUID) (*rimskyattrs.Row, error) {
    var out *rimskyattrs.Row
    err := a.store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
        r, err := a.store.NodeAttributes().GetByRun(ctx, runID, tx)
        if err != nil || r == nil {
            return err
        }
        out = &rimskyattrs.Row{
            RunID:    r.NodeRunID,
            NodeID:   r.NodeID,
            Data:     r.Data,
            UpdatedAt: r.UpdatedAt,
        }
        return nil
    })
    return out, err
}

func (a *attributesStoreAdapter) Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any) error {
    return a.store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
        return a.store.NodeAttributes().Upsert(ctx, runID, nodeID, data, tx)
    })
}

func (a *attributesStoreAdapter) MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any) error {
    return a.store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
        return a.store.NodeAttributes().MergeDelta(ctx, runID, delta, tx)
    })
}
```

### Step 19.3 — Verify (partial)

```
go vet ./runtime/callback.go
```

A full `go build ./runtime/` may still fail because the narrow `graph/attribute/callback.go::NodeAttributeTable` interface (Task 21) hasn't been updated yet. The full build verification gate is at Task 21.6.

**Note:** Tasks 19, 20, and 21 are intentionally coupled — they edit interconnected files. Execute them in sequence without expecting a clean build between them; the build gate is at Task 21.6.

---

## Task 20 — Remove `buildExecuteRequest` prior-read block

**Files:** `runtime/runner_dispatch.go`.

### Step 20.1 — Locate the block

`code:runtime/runner_dispatch.go#714-724`:

```go
var prior *persistence.NodeAttributesRow
if err := dctx.Args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
    p, err := dctx.Args.Persist.NodeAttributes().Get(ctx, acq.NodeID, tx)
    prior = p
    return err
}); err != nil {
    dctx.Args.Logger.Warn("runner_dispatch: read prior node attributes for run_attempt failed", ...)
}
```

Plus the downstream `RunAttempt` assignment.

### Step 20.2 — Delete the block entirely

Remove lines 714-724 and the downstream `RunAttempt` assignment. With `run_attempt` gone from both the proto and the persistence row, there's nothing to populate.

### Step 20.3 — Verify

```
go build ./runtime/
```

Per Task 19's coupling note, this build may still fail until Task 21 lands.

---

## Task 21 — Update `graph/attribute/callback.go` interface + URL route

**Files:** `graph/attribute/callback.go`, plus the chi route mount and supervisor adapter wiring.

### Step 21.1 — Read current file

`code:graph/attribute/callback.go`:
- `Row` struct at `#37` — has `NodeID`, `RunAttempt`, `Data`, `UpdatedAt`.
- `NodeAttributeTable` interface at `#49` — `Get`/`Upsert`/`MergeDelta` keyed by `nodeID`.
- `AuthLookup` at `#59` — `func(token string, nodeID shared.UUID) error`.

### Step 21.2 — Update to per-run

```go
type Row struct {
    RunID     shared.UUID
    NodeID    shared.UUID  // denormalized for forensic queries
    Data      map[string]any
    UpdatedAt time.Time
}

type NodeAttributeTable interface {
    GetByRun(ctx context.Context, runID shared.UUID) (*Row, error)
    Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any) error
    MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any) error
}

type AuthLookup func(token string, runID shared.UUID) error
```

### Step 21.3 — Update HTTP route

Current route: `POST /v1/attributes/{node_id}`. New route:

`POST {callback_url}/v1/runs/{run_id}/attributes`

Update the chi `URLParam(r, "node_id")` → `URLParam(r, "run_id")`. Update the docstring at the top of the file. Update the route mount in whichever supervisor file handles route registration (search `grep -rn "v1/attributes/" runtime/ control/`).

**TypeScript-side cleanup** — the claude-agent executor builds and references this URL in several places. Update all of them:

- `code:executors/claude-agent/src/attributes-tools.ts` — `buildAttributesWritebackUrl(base, nodeId)` at ~#107 constructs `${base}/v1/attributes/${nodeId}`. Rename to `buildAttributesWritebackUrl(base, runId)` and emit `${base}/v1/runs/${runId}/attributes`. Update all internal callers in the same file.
- `code:executors/claude-agent/src/attributes-tools.test.ts` — five tests assert the old URL shape (around #17, #23, #32, #92, #103). Update each to expect the new `/v1/runs/{run_id}/attributes` shape.
- `code:executors/claude-agent/src/token-registry.ts` — docstrings around #31 and #41 reference the old route. Update to reflect the new shape.
- `code:executors/claude-agent/src/agent-run.ts` — comment at #73 and call site at #447 use `buildAttributesWritebackUrl(callbackUrl, nodeId)`. Change the passed identifier from `nodeId` to `runId`. The runID is available via the ExecuteRequest's `dispatch_id` field (proto field 12), which the executor receives in `req.dispatch_id`. Confirm the executor's request-binding code extracts `dispatch_id`; if not, add it.

After the TS edits, the URL shape is consistent across Go and TS. The Go-side test (`go test ./runtime/ -count=1 -run TestCallback`) verifies the chi mount; the TS-side test (`cd executors/claude-agent && npm test`) verifies the URL builder + tests.

### Step 21.4 — Update the cancel_token auth path

The cancel_token at `code:runtime/runner_dispatch.go#713` is `<supervisorID>:<dispatchID>` — already encodes the run identity. `code:runtime/callback.go::attributesAuth#392` parses it inline; no registry lookup.

The current `attributesAuth` flow:
1. Parse the token into `(supervisorID, dispatchID)`.
2. Call `Queue.GetDispatchNode(ctx, dispatchID)` to fetch the node-id this dispatch belongs to.
3. Compare the returned node-id against the URL's `node_id` param (URL-param ownership check).
4. Invoke `AuthLookup(token, nodeID)`.

Under per-run keying, the URL's path param is `run_id` (= dispatchID). The auth path simplifies:

1. Parse the token into `(supervisorID, dispatchID)`.
2. **Drop the `Queue.GetDispatchNode` call.** It's no longer needed — the URL's `run_id` IS the same identifier as the parsed `dispatchID`, so the check collapses to UUID equality between `dispatchID` and the URL's `run_id`. If they don't match, return 403/Unauthorized.
3. Invoke `AuthLookup(token, runID)` where `runID == dispatchID`.

Rationale for dropping `GetDispatchNode`: the cancel_token is a supervisor-issued secret. Knowing the token proves the caller has access to the dispatch. Comparing the parsed dispatchID against the URL run_id closes the URL-spoof attack. The pre-rekeying flow added the `GetDispatchNode` call to resolve `dispatchID → nodeID` because the URL was keyed by `node_id`; under per-run keying the URL is keyed by the same thing the token already encodes, so the resolution step is unnecessary.

There is a separate `CallbackRegistry` at `code:runtime/callback.go#48-51` keyed by `async_ack_id` — that's for async callbacks (different feature) and is NOT touched by this spec.

### Step 21.5 — Update the supervisor adapter

The adapter that wraps `persistence.NodeAttributeTable` for the narrow `attributes.NodeAttributeTable` interface (per Task 19) updates correspondingly. Methods take `runID`; `nodeID` for `Upsert` comes from either the auth context (via runID → run-tree row lookup) or directly from `acq.NodeID` if the supervisor's runtime context still has it.

### Step 21.6 — Verify (full build gate)

```
go build ./...
go test ./runtime/ -count=1 -run TestCallback
```

This is the full build verification for the Tasks 19–21 coupled triplet. Should now pass cleanly.

---

## Task 22 — Update `MarkDrainedBySender` callers in runtime

**Files:** `runtime/runner_terminal.go`, `runtime/on_error.go`, `runtime/sweep_parked.go`.

### Step 22.1 — Update each caller

```
grep -rn "DeleteBySender" runtime/ foundation/
```

Expected callers:
- `code:runtime/runner_terminal.go#536`
- `code:runtime/on_error.go#181`
- `code:runtime/sweep_parked.go#238`
- Conformance tests at `code:foundation/persistence/conformance/wait_set.go` (already updated in Task 12).

For each runtime caller, replace `DeleteBySender` with `MarkDrainedBySender`. The call signature is identical.

### Step 22.2 — Verify

```
go build ./...
go test ./runtime/ -count=1
```

---

## Task 23 — Implement substitution-context builder (minimalist)

**Files:** `runtime/substitution_context.go` (new).

### Step 23.1 — Codebase grounding for the run accessors

Confirm the persistence surface the builder will call:

- The run-tree row type is `persistence.RunTreeRow` at `code:foundation/persistence/run_tree.go#38`. Fields: `RunID, NodeID, FrameID, ParentRunID, ChildKey, State, LastOutcome, AggregationPolicy`. **The run-id field is named `RunID`, not `ID`.**
- The run-tree accessor is `persistence.RunTreeTable` at `code:foundation/persistence/run_tree.go#104`. Get by ID via `args.Persist.RunTree().GetByID(ctx, tx, runID)`.
- `LastOutcome` is the typed string `cascade.LastOutcome` at `code:foundation/cascade/state.go#42`, not raw `string` — comparisons need an explicit `string(...)` cast.
- Node-type lookup: `args.Persist.Nodes().Get(ctx, nodeID, tx)` returns a `*NodeRow` with `NodeType`.

**Do NOT use `args.Persist.NodeRuns()` — that accessor does not exist.** The dispatch-queue surface is `args.Persist.Queue()` (which returns `*persistence.DispatchRow`, a different shape); the run-tree projection is `args.Persist.RunTree()`.

### Step 23.2 — Create the builder

Create `runtime/substitution_context.go`:

```go
// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Substitution-context builder under per-run attribute keying. Resolves
// {{nodes.X.attribute.Y}} directives at dispatch by querying the
// receiver's drained wait-set rows for the current frame (filtered to
// attribute-topic and settled-success senders). Senders not in the
// drained set are absent — the substitution engine returns
// ErrMissingSource for them, and the fallback operator handles the
// receiver-side default.
//
// There is NO scope-walk and NO cross-frame caching. The substitution
// context is exactly "what fired this frame for this receiver." Per
// spec
// .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
// §"Substitution context builder".
//
// @concept: attribute
// @concept: node-run
// @concept: wait-set
package runtime

import (
    "context"
    "encoding/json"

    "github.com/fallguyconsulting/rimsky/foundation/persistence"
    "github.com/fallguyconsulting/rimsky/foundation/shared"
)

// settledSuccessOutcomes is the set of last_outcome values that count
// as "settled success" for substitution-context reads. Failed senders
// (last_outcome='failed') are excluded; their attribute rows are not
// consumed by downstream substitution. Parked senders are filtered by
// the same check: parked terminals drain the wait-set (per
// runtime/runner_terminal_park.go::drainWaitSetOnSettled) but leave
// last_outcome empty per the park-has-no-outcome convention, so they
// fail this set-membership check. The filter is by outcome only and
// works uniformly for failed and parked senders.
var settledSuccessOutcomes = map[string]struct{}{
    "fresh_changed":   {},
    "fresh_unchanged": {},
    "passed":          {},
    "pure_cascade":    {},
}

// BuildAttributeDeps assembles the substitution context's Deps map for
// the receiver's dispatch. Returns a map from sender node-type to the
// sender's attribute row data (as raw JSON for lazy walking).
//
// Steps:
//   1. Query drained wait-set rows for this receiver in this frame,
//      filtered to topic_kind='attribute'.
//   2. For each contributing sender_run_id, check the sender's
//      last_outcome (via RunTree().GetByID); skip non-settled-success
//      senders. Fetch the attribute row via GetByRun. Map by sender's
//      node-type.
//   3. Senders not in the drained set are absent — the substitution
//      engine returns ErrMissingSource for them.
func BuildAttributeDeps(
    ctx context.Context,
    tx persistence.Tx,
    args RunArgs,
    receiverRunID shared.UUID,
    frameID shared.UUID,
) (map[string]json.RawMessage, error) {
    out := make(map[string]json.RawMessage)

    rows, err := args.Persist.WaitSet().ListDrainedAttributeRowsForReceiver(
        ctx, frameID, receiverRunID, tx,
    )
    if err != nil {
        return nil, err
    }
    for _, r := range rows {
        senderRun, err := args.Persist.RunTree().GetByID(ctx, tx, r.SenderRunID)
        if err != nil || senderRun == nil {
            continue
        }
        if _, ok := settledSuccessOutcomes[string(senderRun.LastOutcome)]; !ok {
            continue
        }
        attrRow, err := args.Persist.NodeAttributes().GetByRun(ctx, r.SenderRunID, tx)
        if err != nil || attrRow == nil {
            continue
        }
        nodeType, err := nodeTypeOf(ctx, args, senderRun.NodeID, tx)
        if err != nil || nodeType == "" {
            continue
        }
        raw, err := json.Marshal(attrRow.Data)
        if err != nil {
            continue
        }
        out[nodeType] = raw
    }
    return out, nil
}

// nodeTypeOf resolves a node ID to its node-type via the nodes table.
func nodeTypeOf(ctx context.Context, args RunArgs, nodeID shared.UUID, tx persistence.Tx) (string, error) {
    n, err := args.Persist.Nodes().Get(ctx, nodeID, tx)
    if err != nil || n == nil {
        return "", err
    }
    return n.NodeType, nil
}
```

### Step 23.3 — Verify

```
go build ./runtime/
go test ./runtime/ -count=1
```

---

## Task 24 — Wire `BuildAttributeDeps` into the existing builders

**Files:** `runtime/runner_dispatch.go`, `runtime/runner_locks.go`.

### Step 24.1 — Update `loadSubscribedNodeAttributesByID`

`code:runtime/runner_dispatch.go::loadSubscribedNodeAttributesByID#618` today resolves subscribed senders via the template's static subscription edges, then fetches each sender's attributes by node_id. Replace its body to call `BuildAttributeDeps`:

```go
func loadSubscribedNodeAttributesByID(ctx context.Context, args RunArgs, acq *acquisition) map[string]json.RawMessage {
    var out map[string]json.RawMessage
    if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
        deps, err := BuildAttributeDeps(ctx, tx, args, acq.DispatchID, acq.FrameID)
        if err != nil {
            return err
        }
        out = deps
        return nil
    }); err != nil && args.Logger != nil {
        args.Logger.Warn("loadSubscribedNodeAttributesByID: tx failed",
            "run_id", acq.DispatchID.String(), "error", err.Error())
    }
    return out
}
```

### Step 24.2 — Update lock-substitution path

`code:runtime/runner_locks.go#117` uses `loadSubscribedNodeAttributes(ctx, args, tx, subs)`. Replace with a call to `BuildAttributeDeps(ctx, tx, args, acq.DispatchID, acq.FrameID)` — same parameters, same semantics.

The lock-substitution path runs at acquisition phase, but by then the wait-set is settled (rows drained) — that's what made the receiver eligible. The same builder works for both phases.

### Step 24.3 — Delete the old `loadSubscribedNodeAttributes`

The function at `runtime/runner_locks.go:379` is superseded. Delete it. Verify no other callers via `grep -rn "loadSubscribedNodeAttributes"`.

### Step 24.4 — Decide the fate of `resolveSubscribedSenders`

`resolveSubscribedSenders` at `code:runtime/subscription_loaders.go#79` returns the list of upstream sender **node IDs** for a receiver. Under the new substitution-context builder, this function is no longer needed for substitution. Grep for remaining callers:

```
grep -rn "resolveSubscribedSenders" runtime/ control/
```

If only the substitution-context builders used it, delete. If other callers remain (cascade walker, lineage writer), leave it.

### Step 24.5 — Verify

```
go build ./runtime/
go test ./runtime/ -count=1
```

---

## Task 25 — Add `hard_dep` flag to schema decode and template validator

**Files:** `graph/node/template_validator.go`.

### Step 25.1 — Schema-decode (no spec change)

The attribute schema is parsed as `map[string]any` (untyped JSON Schema). No struct change in `foundation/spec/template.go` — the validator recognizes the flag.

### Step 25.2 — Extend the validator

Read `code:graph/node/template_validator.go::checkAttributeSource#851` and the surrounding schema-property walker (around #820+).

Update the per-property walker to recognize the `hard_dep` key:

```go
for fname, raw := range properties {
    propMap, ok := raw.(map[string]any)
    if !ok {
        continue
    }
    srcRaw, ok := propMap["source"]
    if !ok {
        continue
    }
    src, ok := srcRaw.(string)
    if !ok {
        res.Errors = append(res.Errors, ValidationError{
            Path: fmt.Sprintf("%s.properties.%s.source", sbase, fname),
            Msg:  "source must be a string",
        })
        continue
    }
    checkAttributeSource(src, fmt.Sprintf("%s.properties.%s.source", sbase, fname), declared, directAliases, inheritedAliases, res)

    // Validate hard_dep, if present.
    if hd, present := propMap["hard_dep"]; present {
        hdBool, ok := hd.(bool)
        if !ok {
            res.Errors = append(res.Errors, ValidationError{
                Path: fmt.Sprintf("%s.properties.%s.hard_dep", sbase, fname),
                Msg:  "hard_dep must be a boolean",
            })
        } else if hdBool {
            // hard_dep only applies to nodes.<X>.attribute.<Y> reads.
            // Reject the flag on other source kinds (claim, params,
            // trigger, child, event) — those are intrinsically per-frame
            // or instance-scoped and the flag is meaningless.
            if !isAttributeSourceDirective(src) {
                res.Errors = append(res.Errors, ValidationError{
                    Path: fmt.Sprintf("%s.properties.%s.hard_dep", sbase, fname),
                    Msg:  "hard_dep applies only to nodes.<X>.attribute.<Y> sources; other source kinds are intrinsically per-frame or instance-scoped and don't admit hard_dep",
                })
            }
        }
    }
}
```

Add helper:

```go
// isAttributeSourceDirective returns true if src is a {{...}} directive
// whose body starts with "nodes.<X>.attribute". Used by the validator
// to gate the hard_dep flag.
func isAttributeSourceDirective(src string) bool {
    trimmed := strings.TrimSpace(src)
    m := dispatchDirectiveRe.FindStringSubmatch(trimmed)
    if m == nil {
        return false
    }
    body := strings.TrimSpace(m[1])
    parts := strings.SplitN(body, ".", 3)
    return len(parts) >= 3 && parts[0] == "nodes" && parts[2] == "attribute"
}
```

`dispatchDirectiveRe` is accessible from the same package (`code:graph/node/template_validator.go#49`).

### Step 25.3 — Verify

```
go build ./graph/node/
go test ./graph/node/ -count=1 -run TestValidate
```

---

## Task 26 — Implement `BuildHardDepEdges` and cycle detection

**Files:** `graph/node/hard_dep_edges.go` (new), `graph/node/hard_dep_edges_test.go` (new), `graph/node/template_validator.go` (wire-in).

### Step 26.1 — Create the edge-map computation

Create `graph/node/hard_dep_edges.go`:

```go
// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Hard-dep edge map. Computed at template registration alongside the
// subscription-edge map (subscription_edges.go); consumed by the
// cascade walker at runtime to proactively invalidate upstreams for
// hard_dep attribute reads.
//
// Note the key-direction difference from SubscriptionEdgeMap:
// subscription edges are keyed by SENDER (downstream lookup from a
// transitioning sender); hard-dep edges are keyed by RECEIVER (upstream
// lookup from a freshly-invalidated receiver). The divergence is
// intentional per spec §"hard-dep cascade extension".
//
// Per .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
// §"hard_dep flag" and §"hard-dep cascade extension".
//
//  @concept: attribute
//  @concept: cascade
package node

import (
    "fmt"
    "strings"

    "github.com/fallguyconsulting/rimsky/foundation/spec"
)

// HardDepEdgeMap is keyed by receiver node-type. The value is the
// list of upstream node-types the receiver requires invalidated in
// the same frame.
type HardDepEdgeMap map[string][]string

// BuildHardDepEdges walks every node's attribute schema looking for
// fields with `hard_dep: true` and a `source:` referencing
// `{{nodes.X.attribute.Y}}`. Produces a map from receiver node-type
// to the set of sender node-types. Performs cycle detection on the
// resulting graph; returns an error describing the cycle if found.
// Soft-dep cycles (without hard_dep) are not in this graph.
func BuildHardDepEdges(tmpl spec.TemplateSpec) (HardDepEdgeMap, error) {
    out := HardDepEdgeMap{}
    for _, n := range tmpl.Nodes {
        senders := hardDepSendersOf(n)
        if len(senders) > 0 {
            out[n.Type] = senders
        }
    }
    if err := detectCycle(out); err != nil {
        return nil, err
    }
    return out, nil
}

// hardDepSendersOf returns the upstream node-types referenced by
// `hard_dep: true` attribute reads in n's schema. Excludes
// self-references (trivially cyclic).
func hardDepSendersOf(n TemplateNodeDef) []string {
    if n.Attributes == nil || len(n.Attributes.Schema) == 0 {
        return nil
    }
    props, _ := n.Attributes.Schema["properties"].(map[string]any)
    if props == nil {
        return nil
    }
    seen := make(map[string]struct{})
    var out []string
    for _, raw := range props {
        propMap, ok := raw.(map[string]any)
        if !ok {
            continue
        }
        hd, _ := propMap["hard_dep"].(bool)
        if !hd {
            continue
        }
        src, _ := propMap["source"].(string)
        if src == "" {
            continue
        }
        refs := substitutionDirectiveRe.FindAllStringSubmatch(src, -1)
        for _, m := range refs {
            body := strings.TrimSpace(m[1])
            ref, ok := parseSubstitutionDirective(body)
            if !ok {
                continue
            }
            if ref.SenderNodeType == "" || ref.SenderNodeType == n.Type {
                continue // skip self
            }
            if ref.TopicKind != "attribute" {
                continue // only attribute reads hard-dep
            }
            if _, dup := seen[ref.SenderNodeType]; dup {
                continue
            }
            seen[ref.SenderNodeType] = struct{}{}
            out = append(out, ref.SenderNodeType)
        }
    }
    return out
}

// detectCycle does a DFS over the hard-dep edge graph and returns a
// descriptive error if a cycle is found.
func detectCycle(edges HardDepEdgeMap) error {
    const (
        white = 0
        gray  = 1
        black = 2
    )
    color := make(map[string]int)
    var path []string

    var dfs func(node string) error
    dfs = func(node string) error {
        color[node] = gray
        path = append(path, node)
        for _, next := range edges[node] {
            switch color[next] {
            case gray:
                // Cycle detected. Copy path before appending to avoid
                // sharing backing array with the live `path` slice.
                cyclePath := append([]string(nil), path...)
                cyclePath = append(cyclePath, next)
                return fmt.Errorf("hard-dep cycle detected: %v", cyclePath)
            case white:
                if err := dfs(next); err != nil {
                    return err
                }
            }
        }
        path = path[:len(path)-1]
        color[node] = black
        return nil
    }

    for receiver := range edges {
        if color[receiver] == white {
            if err := dfs(receiver); err != nil {
                return err
            }
        }
    }
    return nil
}
```

The `substitutionDirectiveRe` and `parseSubstitutionDirective` symbols are package-private in `code:graph/node/subscription_edges.go` (#104 and #234 respectively) and accessible from `hard_dep_edges.go` since both are in `package node`.

### Step 26.2 — Wire into the template validator

In `graph/node/template_validator.go`, find where `BuildSubscriptionEdges` is called at registration. Add a parallel call to `BuildHardDepEdges`; if it returns an error, surface as a `ValidationError`:

```go
if _, err := BuildHardDepEdges(tmpl); err != nil {
    res.Errors = append(res.Errors, ValidationError{
        Path: "graphs",
        Msg:  err.Error(),
    })
}
```

### Step 26.3 — Tests in `hard_dep_edges_test.go`

- `TestBuildHardDepEdges_NoHardDep` — template with no hard_dep, returns empty map.
- `TestBuildHardDepEdges_SimpleHardDep` — A → B hard_dep read, returns `{A: [B]}`.
- `TestBuildHardDepEdges_SelfReferenceIgnored` — A reads itself with hard_dep, excluded.
- `TestBuildHardDepEdges_CycleDetected` — A hard-deps B, B hard-deps A; expect cycle error.
- `TestBuildHardDepEdges_NonAttributeKindIgnored` — `hard_dep: true` on a `claim.X.payload` source; not in the edge map (validator rejects, but tests the BuildHardDepEdges path defensively).

### Step 26.4 — Verify

```
go build ./graph/node/
go test ./graph/node/ -count=1 -run TestBuildHardDep
```

---

## Task 27 — Cache the hard-dep edge map alongside the subscription-edge cache

**Files:** `runtime/subscription_loaders.go`.

### Step 27.1 — Read the existing cache shape

The current subscription-edge cache at `code:runtime/subscription_loaders.go#43` uses a flat `sync.Map`:

```go
var templateSubscriptionEdges sync.Map // map[string]node.SubscriptionEdgeMap
```

with a paired loader `subscriptionEdgesForTemplate(ctx, args, templateHash, tx)` at #48. **No enclosing struct.** Mirror this exact pattern for hard-dep edges.

### Step 27.2 — Add parallel hard-dep edge cache

In the same file, add:

```go
// templateHardDepEdges caches the per-template hard-dep edge map
// (computed from attribute schema fields with hard_dep: true) so
// the cascade walker can look up hard deps without re-parsing the
// template spec on every walk.
//
// @concept: attribute
// @concept: cascade
var templateHardDepEdges sync.Map // map[string]node.HardDepEdgeMap

// hardDepEdgesForTemplate returns the cached or freshly-built
// hard-dep edge map for templateHash. Returns an error if the
// template's hard-dep edge graph contains a cycle (caught at
// registration; surfaced here as a defensive re-check).
func hardDepEdgesForTemplate(
    ctx context.Context, args RunArgs, templateHash string, tx persistence.Tx,
) (node.HardDepEdgeMap, error) {
    if v, ok := templateHardDepEdges.Load(templateHash); ok {
        return v.(node.HardDepEdgeMap), nil
    }
    tmpl, err := args.Persist.Templates().GetByHash(ctx, templateHash, tx)
    if err != nil {
        return nil, fmt.Errorf("hardDepEdgesForTemplate: get template %s: %w", templateHash, err)
    }
    if tmpl == nil {
        return nil, fmt.Errorf("hardDepEdgesForTemplate: template %s not found", templateHash)
    }
    edges, err := node.BuildHardDepEdges(tmpl.Spec)
    if err != nil {
        return nil, err
    }
    actual, _ := templateHardDepEdges.LoadOrStore(templateHash, edges)
    return actual.(node.HardDepEdgeMap), nil
}
```

`Templates().GetByHash` exists at `code:foundation/persistence/templates.go#52`. Match the error-wrap style of `subscriptionEdgesForTemplate`.

### Step 27.3 — Verify

```
go build ./runtime/
```

---

## Task 28 — Extend the cascade walker to process hard-dep edges

**Files:** `runtime/runner_terminal.go`.

### Step 28.1 — Study the existing walker

The cascade walker site is `cascadeSubscribersStaleInTx` at `code:runtime/runner_terminal.go#372` (NOT `walkCascadeForInvalidatedNode` in cascade_invalidate.go, which is a thin wrapper that calls `cascadeSubscribersStaleInTx`).

In-scope variables when the walker reaches the FrameIn branch:

- `senderNodeType`, `senderRunID`, `senderFrameID` — passed in.
- `inst` — `*persistence.InstanceRow` loaded at line 380; `inst.TemplateHash` is the template hash.
- `edges` — `node.SubscriptionEdgeMap` from `subscriptionEdgesForTemplate(ctx, args, inst.TemplateHash, tx)` at line 388.
- `byType map[string][]persistence.NodeRow` — instance nodes grouped by node-type, built around line 401.
- The BFS walks `byType[receiverType]` and inserts wait-set rows for each receiver via `args.Persist.WaitSet().Insert(...)`. The BFS resolves the receiver's in-flight run via `queue.GetInFlightRunForNode(ctx, tx, r.ID, senderFrameID)` (call site around line 490).

The walker runs entirely inside a caller-owned transaction. The hard-dep walk MUST NOT call `InvalidateNode` (which opens its own transactions and would self-deadlock — see `code:runtime/cascade_invalidate.go#217-223`). All work happens inline within the existing `tx`.

### Step 28.2 — Locate the BFS body and add the hard-dep walk

Integrate into the **FrameIn branch** (the default branch where the wait-set Insert happens; not the FrameNext branch which enqueues into a coalesced next frame and does no in-frame wait-set work). The hard-dep walk only applies to FrameIn because hard-dep gating is meaningless when the receiver isn't dispatching in the current frame.

After the existing logic that marks a receiver stale and inserts a wait-set row for the (receiver, sender) pair, add a hard-dep walk for that receiver:

```go
// Hard-dep pull: for each hard_dep attribute read the receiver
// declares, ensure the upstream has an in-flight run in this frame
// and a wait-set blocker on the receiver.
hardEdges, err := hardDepEdgesForTemplate(ctx, args, inst.TemplateHash, tx)
if err != nil {
    return fmt.Errorf("cascadeSubscribersStaleInTx: hard-dep edges: %w", err)
}
for _, upstreamType := range hardEdges[r.NodeType] {
    upstreamNodes := byType[upstreamType]
    if len(upstreamNodes) == 0 {
        continue // defensive: template validator should have caught
    }
    upstreamNode := upstreamNodes[0] // one node per type per instance

    upstreamRunID, hasRun, err := args.Queue.GetInFlightRunForNode(
        ctx, tx, upstreamNode.ID, senderFrameID,
    )
    if err != nil {
        return fmt.Errorf("cascadeSubscribersStaleInTx: get in-flight upstream %s: %w",
            upstreamType, err)
    }

    if !hasRun {
        // Inline stale-mark + recursive cascade walk. Do NOT call
        // InvalidateNode — it opens its own tx and would self-
        // deadlock (see runtime/cascade_invalidate.go#212-268 and
        // the deadlock-warning comment at #217-223).
        //
        // Canonical in-tx sequence (mirror it in
        // stalemarkAndEnqueueInFrame):
        //   1. args.Persist.Nodes().MarkStaleForCascade(ctx,
        //          upstreamNode.ID, senderFrameID, tx)
        //      — atomic stale-mark + run-row insert at
        //        code:foundation/persistence/nodes.go::NodeTable.MarkStaleForCascade#116
        //   2. args.Persist.Events().Append(ctx, EventAppendInput{...})
        //      — emit `state_transition` event with reason
        //        `hard_dep_pull`, frame_id=senderFrameID. Mirror
        //        invalidateInFrame's audit event shape.
        //   3. walkCascadeForInvalidatedNode(ctx, args.Persist,
        //          args.Queue, tx, args.Logger, upstreamNode.ID,
        //          upstreamNode.InstanceID, senderFrameID)
        //      — recurse into the cascade walk for the just-stale
        //        upstream so its own subscribers also gate on it.
        //        The wait-set Insert is idempotent via
        //        ON CONFLICT DO NOTHING on PK
        //        (code:foundation/persistence/postgres/wait_set.go#39),
        //        so re-entering the walker is safe.
        //
        // Recursion choice: the helper MUST call
        // walkCascadeForInvalidatedNode. Skipping the recursion
        // would gate the upstream itself but leave its own
        // subscribers ungated within this frame, breaking the
        // cascade's single-frame-drain property.
        if err := stalemarkAndEnqueueInFrame(
            ctx, args, tx, upstreamNode, senderFrameID,
        ); err != nil {
            return fmt.Errorf("cascadeSubscribersStaleInTx: stale-mark upstream %s: %w",
                upstreamType, err)
        }
        upstreamRunID, hasRun, err = args.Queue.GetInFlightRunForNode(
            ctx, tx, upstreamNode.ID, senderFrameID,
        )
        if err != nil || !hasRun {
            return fmt.Errorf("cascadeSubscribersStaleInTx: upstream %s not in-flight after stale-mark",
                upstreamType)
        }
    }

    // Insert wait-set blocker for the receiver on this upstream's run.
    if err := args.Persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
        FrameID:           senderFrameID,
        ReceiverRunID:     receiverRunID,
        SenderRunID:       upstreamRunID,
        TopicKind:         "attribute",
        SubscriptionScope: "direct",
    }, tx); err != nil {
        return fmt.Errorf("cascadeSubscribersStaleInTx: insert hard-dep wait-set: %w", err)
    }
}
```

`stalemarkAndEnqueueInFrame` is a new in-tx helper. Signature: `stalemarkAndEnqueueInFrame(ctx context.Context, args RunArgs, tx persistence.Tx, target *persistence.NodeRow, frameID shared.UUID) error`. Use `RunArgs` (not `InvalidateArgs`) for the args type because the caller context (`cascadeSubscribersStaleInTx`) holds `RunArgs`. `RunArgs` exposes `Persist`, `Queue`, and `Logger` — the only fields the helper needs (matching the subset of `InvalidateArgs` that `invalidateInFrame`'s body uses).

Place the helper in `runtime/cascade_invalidate.go` next to `invalidateInFrame`. Body mirrors `invalidateInFrame`'s in-tx sequence (`code:runtime/cascade_invalidate.go#212-268`) — `args.Persist.Nodes().MarkStaleForCascade(ctx, target.ID, frameID, tx)` + `args.Persist.Events().Append(ctx, EventAppendInput{state_transition, reason=hard_dep_pull}, tx)` + `walkCascadeForInvalidatedNode(ctx, args.Persist, args.Queue, tx, args.Logger, target.ID, target.InstanceID, frameID)` — but accepts a pre-existing `tx persistence.Tx` rather than opening its own transaction. Read `invalidateInFrame` end-to-end before copying.

### Step 28.3 — Verify

```
go build ./runtime/
go test ./runtime/ -count=1 -run TestCascade
go test ./runtime/ -race -count=3 -run TestCascade
```

---

## Task 29 — Implement the fallback operator in substitution

**Files:** `graph/attribute/substitution.go`, `graph/attribute/substitution_test.go`, `graph/node/template_validator.go`, `graph/node/template_validator_test.go`.

### Step 29.1 — Update directive parsing

Read `code:graph/attribute/substitution.go::resolveDirectiveValue#268`. Add support for the `|` infix.

Pseudocode for `resolveDirectiveValue`:

```go
func resolveDirectiveValue(directive string, ctx ResolveContext) (any, error) {
    if idx := strings.Index(directive, "|"); idx >= 0 {
        leftRaw := strings.TrimSpace(directive[:idx])
        rightRaw := strings.TrimSpace(directive[idx+1:])
        // Reject multi-pipe chains
        if strings.Contains(rightRaw, "|") {
            return nil, fmt.Errorf("fallback chains not admitted: %q", directive)
        }
        val, err := resolveDirectiveValueRaw(leftRaw, ctx)
        if err == nil {
            return val, nil
        }
        if !IsMissingSource(err) {
            return nil, err // non-missing error is fatal
        }
        return parseLiteral(rightRaw)
    }
    return resolveDirectiveValueRaw(directive, ctx)
}

func parseLiteral(raw string) (any, error) {
    if raw == "null" {
        return nil, nil
    }
    if raw == "true" {
        return true, nil
    }
    if raw == "false" {
        return false, nil
    }
    if n, err := strconv.ParseFloat(raw, 64); err == nil {
        return n, nil
    }
    if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
        var s string
        if err := json.Unmarshal([]byte(raw), &s); err == nil {
            return s, nil
        }
    }
    return nil, fmt.Errorf("invalid literal in fallback: %q", raw)
}
```

Where `resolveDirectiveValueRaw` is the renamed existing `resolveDirectiveValue` body (the one that dispatches on `nodes.` / `claim.` / `params.` / etc.).

### Step 29.2 — Update the validator

In `graph/node/template_validator.go::checkAttributeSource`, update the parser to admit `<directive> | <literal>` as a valid `source:` shape. After parsing the outer `{{...}}` envelope and getting the body, check for `|` and validate each side. The left must be one of the closed source kinds; the right must parse as a JSON literal (one of `null`, `true`, `false`, a number, or a quoted string). Reject multi-`|` chains.

### Step 29.3 — Add tests

In `graph/attribute/substitution_test.go`:

- `TestFallbackOperator_DirectiveResolves` — `{{nodes.X.attribute.Y | "default"}}` resolves to nodes.X.attribute.Y when present.
- `TestFallbackOperator_DirectiveMissing_FallsThrough` — same directive, X has no run; resolves to "default".
- `TestFallbackOperator_NullLiteral` — `{{X | null}}` returns nil when X missing.
- `TestFallbackOperator_NumberLiteral` — `{{X | 42}}` returns 42.0 when X missing.
- `TestFallbackOperator_BoolLiteral` — `{{X | true}}` returns true when X missing.
- `TestFallbackOperator_NonMissingErrorIsFatal` — directive returns a non-missing error; literal NOT used; error propagates.
- `TestFallbackOperator_ChainsRejected` — `{{X | Y | Z}}` fails to parse.
- `TestFallbackOperator_ObjectLiteralRejected` — `{{X | {}}}` fails to parse.

In `graph/node/template_validator_test.go`:

- `TestValidator_FallbackOperator_Valid` — template with `{{X | "default"}}` validates clean.
- `TestValidator_FallbackOperator_ChainsRejected` — template with `{{X | Y | Z}}` raises a ValidationError.

### Step 29.4 — Verify

```
go build ./graph/attribute/ ./graph/node/
go test ./graph/attribute/ -count=1
go test ./graph/node/ -count=1
```

---

## Task 30 — Complete the subgraph carry-rule (write parent's attribute row)

**Files:** `runtime/subgraph_dispatch.go`.

### Step 30.1 — Understand the current state

Read `code:runtime/subgraph_dispatch.go::CarryExitWriteback#170-239` and `::applyTerminalCompleteSubgraphExit#606-652` in full.

The current behavior:

- `CarryExitWriteback` loads the exit run, loads the parent run, validates the writeback bytes are JSON-decodable, logs, and returns. It does NOT call `NodeAttributes().Upsert` (no access to the table via `PropagationArgs`).
- `applyTerminalCompleteSubgraphExit` opens a tx, calls `CarryExitWriteback`, emits a `subgraph.exit_carry` audit event, and returns. It also does NOT call `NodeAttributes().Upsert`.

The result: the subgraph's exit writeback is validated and logged but never persisted. The blessed-invariant docstring at `#605` (*"exit-node-writeback flows to parent run writeback"*) is aspirational, not implemented. Downstream consumers subscribed to the calling node see `ErrMissingSource` on `{{nodes.SC.attribute.foo}}` reads because SC's attribute row never receives the exit's bytes.

Under per-run keying with this-frame-only substitution (per the spec), this is a hard failure for subgraph composition — without the carry-rule actually persisting, subgraphs are leaky abstractions. This task completes the carry-rule by adding an explicit `Upsert` after `CarryExitWriteback` validates.

### Step 30.2 — Add the Upsert in `applyTerminalCompleteSubgraphExit`

Edit `applyTerminalCompleteSubgraphExit` to load the parent run after `CarryExitWriteback` and Upsert the parent's attribute row with the exit's writeback bytes (already marshalled as `merged map[string]any`). Both reads/writes happen inside the same caller-owned transaction.

Updated body (replace the existing `return args.Persist.Transaction(...)` block):

```go
return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
    if err := CarryExitWriteback(ctx, PropagationArgs{
        RunTree:      args.Persist.RunTree(),
        ClaimHandles: args.ClaimHandles,
        Logger:       args.Logger,
    }, tx, acq.DispatchID, wb); err != nil {
        return err
    }

    // Carry the exit's writeback to the parent run's attribute row.
    // The blessed-invariant at #605 ("exit-node-writeback flows to
    // parent run writeback") requires the parent's row to contain the
    // exit's bytes so downstream consumers reading
    // {{nodes.<calling-node>.attribute.<field>}} see the subgraph's
    // output. CarryExitWriteback only validates + logs; the Upsert
    // lives here because the caller has NodeAttributeTable in scope.
    exit, err := args.Persist.RunTree().GetByID(ctx, tx, acq.DispatchID)
    if err != nil {
        return fmt.Errorf("applyTerminalCompleteSubgraphExit: load exit run: %w", err)
    }
    if exit == nil || exit.ParentRunID == nil {
        return fmt.Errorf("applyTerminalCompleteSubgraphExit: exit run %s has no parent", acq.DispatchID)
    }
    parent, err := args.Persist.RunTree().GetByID(ctx, tx, *exit.ParentRunID)
    if err != nil {
        return fmt.Errorf("applyTerminalCompleteSubgraphExit: load parent run %s: %w", *exit.ParentRunID, err)
    }
    if parent == nil {
        return fmt.Errorf("applyTerminalCompleteSubgraphExit: parent run %s not found", *exit.ParentRunID)
    }
    if err := args.Persist.NodeAttributes().Upsert(
        ctx, parent.RunID, parent.NodeID, merged, tx,
    ); err != nil {
        return fmt.Errorf("applyTerminalCompleteSubgraphExit: upsert parent attributes: %w", err)
    }

    // Forensics: emit `subgraph.exit_carry` for the carry-rule. The
    // parent run row is already loaded; reuse instead of re-fetching.
    nodeID := acq.NodeID
    instanceID := acq.InstanceID
    return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
        NodeID:     &nodeID,
        InstanceID: &instanceID,
        Kind:       "subgraph.exit_carry",
        Payload: map[string]any{
            "parent_run_id":   exit.ParentRunID.String(),
            "exit_run_id":     acq.DispatchID.String(),
            "exit_node_alias": acq.NodeType,
            "outcome":         "fresh",
        },
    }, tx)
})
```

Note that the existing body had a second `RunTree().GetByID(ctx, tx, acq.DispatchID)` call further down for the event-emit; the rewrite hoists that load to before the Upsert and reuses the result. The exit's own attribute row is NOT written — it stays empty, which is correct (exit is internal to the subgraph and not externally addressable).

### Step 30.3 — Update the `CarryExitWriteback` comment block

The inline comment at `code:runtime/subgraph_dispatch.go#228-231` previously hinted that the caller "invokes its own NodeAttributes().Upsert" — but no caller actually did. Update the comment to reflect the new fix:

```
// CarryExitWriteback validates the exit's writeback bytes and emits
// an audit log. The caller (applyTerminalCompleteSubgraphExit)
// performs the actual NodeAttributes().Upsert against the parent
// run's row so the subgraph's output is observable through the
// calling node's attribute surface per concept:delegation's
// "exit-node-writeback flows to parent run writeback" rule.
```

The log line at `#233-237` already logs `parent_run_id` and `parent_node_id`. No change to the log.

### Step 30.4 — Verify

```
go build ./runtime/
go test ./runtime/ -count=1 -run TestSubgraph
```

The Task 37 scenario test (updated in this revision) explicitly exercises the carry: each subgraph invocation's parent attribute row contains the exit's writeback, isolated by per-run keying. That's the load-bearing end-to-end check.

### Step 30.5 — Sanity check: the parent Upsert actually lands

```
grep -nF 'NodeAttributes().Upsert' runtime/subgraph_dispatch.go
```

Expect at least one match inside `applyTerminalCompleteSubgraphExit` (around the function body). If 0, the implementer dropped the carry-fix.

---

## Task 31 — Update `concepts/attribute.md`

**Files:** `.ok-planner/design/concepts/attribute.md`.

### Step 31.1 — Read current file

Read in full.

### Step 31.2 — Replace the closed-grammar invariant

Find the Invariant bullet that reads "The substitution grammar is a closed enumeration of source kinds: ..." (around line 29). Replace with:

```
- The substitution grammar is a closed enumeration of source kinds: `nodes.<X>.attribute.<field-path>`, `nodes.<X>.event.<name>.<field-path>`, `claim.<alias>.{address|scope|payload.<field-path>}`, `params.<field-path>`, `trigger.message.payload.<field-path>`, `child.partition_key`. Each path-walking kind admits an optional-empty trailing path; with an empty trailing path the directive resolves to the kind's JSON root. Resolution is either whole-directive (the input is exactly one `{{...}}` directive modulo whitespace; returns the JSON value verbatim) or embedded (the input has literal text alongside directives; stringifies and concatenates). The grammar also admits a fallback operator: `{{<directive> | <literal>}}` returns the directive's value if present, else the literal (one of `null`, `true`, `false`, a JSON number, or a quoted string). Multi-directive chains (`{{X | Y | Z}}`) and composite literals (objects, arrays) are not admitted. The legacy `deps.<X>.<Y>` form is retired and rejected with a migration-pointer error.
```

### Step 31.3 — Append new Invariants

After the "Errors omit value bytes ..." bullet, append:

```
- Attribute storage is per-run, keyed by `node_run_id` (foreign key to `rimsky_node_runs` with `ON DELETE CASCADE`). A denormalized `node_id` column supports forensic / observability lookups via `GetLatestByNode`; the dispatch-time substitution path uses `GetByRun` against wait-set sender_run_ids that contributed to this dispatch in this frame.
- Per-field `source:` admits an opt-in `hard_dep: true` flag on `nodes.<X>.attribute.<Y>` reads. When set, the cascade walker proactively invalidates the upstream so its value is available in the current frame. Hard-dep cycles are rejected at template registration via `BuildHardDepEdges`.
- Substitution reads are scoped to the current frame. A `{{nodes.X.attribute.Y}}` directive resolves to the X-run that contributed to this dispatch via the frame's wait-set; reads of X-runs from earlier frames return `ErrMissingSource`. `rimsky_node_attributes` rows are the persistent record of what each node-run produced — not a cache. State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs.
```

### Step 31.4 — Append a new Boundaries paragraph

After the existing "Clarifying note on arity" paragraph (added by the 2026-05-20 multi-source decline spec):

```
Clarifying note on subgraph sealing: subgraphs are sealed. Internal nodes can read from siblings of the same invocation, the calling node's attributes, and the always-available source kinds (`params`, claims, trigger messages, `child.partition_key`) — but not from upstream nodes in the calling graph by free reference. The calling graph's namespace is not visible inside the subgraph. Authors thread calling-graph state through the calling node explicitly.
```

### Step 31.5 — Insert a new `## Non-goals` section between `## Boundaries` and `## Invariants`

Section content (exact text):

```
## Non-goals

Patterns considered carefully during platform design and **decided against**. These are positions, not deferrals — future agents reaching for these patterns should argue against this section's rationale rather than treating them as open backlog.

- **Cross-frame attribute caching.** A `{{nodes.X.attribute.Y}}` read at receiver R's dispatch resolves only against the X-run that contributed to R's dispatch via this frame's wait-set. Reads of X-runs from earlier frames return `ErrMissingSource`. `rimsky_node_attributes` rows are the persistent record of what each node-run produced — not a cache. State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs.
- **Function-form substitution grammar.** No `{{coalesce(X, Y)}}`, `{{newest(X, Y)}}`, `{{merge(X, Y)}}`, or other in-grammar functions. The grammar stays a closed enumeration of source-kind directives plus an optional literal fallback. Aggregation and transformation logic lives in receiver executors, not in the substitution layer.
- **Multi-directive fallback chains.** The fallback operator `{{<directive> | <literal>}}` admits exactly one directive on the left and exactly one JSON literal (`null`, boolean, number, or quoted string) on the right. Multi-directive chains (`{{X | Y | Z}}`) and composite literals (`{}`, `[]`) are not admitted.
- **Closure semantics for subgraphs.** Subgraph internal nodes cannot read attributes from upstream nodes in the calling graph by free reference (see Boundaries above). Calling-graph state threads through the calling node explicitly.
- **`force_fresh: true` (always-re-execute), `pull_only: true` (suppress auto-subscribe), `trigger_if_missing: true` (lazy upstream initialization).** None of these flags exist. The configuration surface is exactly `hard_dep: true` on attribute schema properties whose source is `{{nodes.<X>.attribute.<Y>}}`.

See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md` for the brainstorm rationale per item.
```

### Step 31.6 — Append a Notes entry

At the bottom of the `## Notes` section:

```
- 2026-05-20 — Per-run keying lift + minimalist substitution model. `rimsky_node_attributes` re-keyed from `node_id` to `node_run_id`, completing the 2026-05-15 run-tree extension's "all state-bearing columns" intent. Substitution context at dispatch reads only drained wait-set rows for this receiver in this frame (topic_kind='attribute', settled-success senders); no scope-walk, no cross-frame caching. Per-field `hard_dep: true` flag opt-in for "ensure upstream is invalidated in this frame," with cascade-walker proactive invalidation via `BuildHardDepEdges`. Fallback operator `{{<directive> | <literal>}}` added. New `## Non-goals` section above captures load-bearing decisions about what this concept deliberately does NOT support. The 2026-05-20 multi-source decline (per-field arity 1) remains intact — the fallback operator is "exactly one directive + one literal," not multi-source. See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`.
```

### Step 31.7 — Verify

```
grep -cF '## Non-goals' .ok-planner/design/concepts/attribute.md
grep -cF 'hard_dep: true' .ok-planner/design/concepts/attribute.md
grep -cF 'Clarifying note on subgraph sealing' .ok-planner/design/concepts/attribute.md
grep -cF '2026-05-20 — Per-run keying lift' .ok-planner/design/concepts/attribute.md
```

Each should return at least `1`.

---

## Task 32 — Update `concepts/node-run.md`

**Files:** `.ok-planner/design/concepts/node-run.md`.

### Step 32.1 — Update "all state-bearing columns" framing

Find the language describing state-bearing columns lifted off `rimsky_nodes`. Add a clarifying sentence noting that `rimsky_node_attributes` is now per-run via FK, completing the lift.

### Step 32.2 — Append Notes entry

At the bottom of `## Notes`:

```
- 2026-05-20 — Per-run attribute lift complete. `rimsky_node_attributes` re-keyed from `node_id` to `node_run_id` with cascade delete via the run row. The 2026-05-15 "all state-bearing columns" claim is now literally true (modulo derived caches). See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`.
```

### Step 32.3 — Verify

```
grep -cF '2026-05-20 — Per-run attribute lift complete' .ok-planner/design/concepts/node-run.md
```

Returns `1`.

---

## Task 33 — Update `concepts/wait-set.md`

**Files:** `.ok-planner/design/concepts/wait-set.md`.

### Step 33.1 — Update Invariants for drain semantics

Find the Invariant describing drain. Today: "drain deletes rows." Replace with: "drain marks `drained_at = NOW()` on rows where `sender_run_id` matches the settling sender. Drained rows remain queryable for the substitution-context builder. Eligibility predicate: a stale run is dispatch-eligible iff no `drained_at IS NULL` rows exist for it in the current frame."

### Step 33.2 — Correct stale PK enumeration

The current doc describes the PK as `(frame_id, receiver_node_id, sender_node_id, ...)` — stale; the actual schema PK is `(frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)` per `code:foundation/persistence/postgres/migrations/001-baseline.sql#239`. Update the doc to match the actual schema (adding `drained_at` as a non-PK column visible to substitution-context queries).

### Step 33.3 — Append Boundaries paragraph

```
Drained rows are the durable record of "which senders contributed to this receiver's dispatch in this frame." The substitution-context builder queries them (filtered to `topic_kind='attribute'`, with sender state checked against settled-success outcomes) to populate the `Deps` map for `{{nodes.X.attribute.Y}}` directives. Cleanup happens via frame cascade-delete.
```

### Step 33.4 — Append Notes entry

```
- 2026-05-20 — Mark-don't-delete on drain. New `drained_at TIMESTAMPTZ` column on `rimsky_wait_set`; drain marks rather than deletes; eligibility predicate updates to "no undrained rows." `DeleteBySender` renamed to `MarkDrainedBySender`. New `ListDrainedAttributeRowsForReceiver` accessor for the substitution-context builder. PK enumeration in this file corrected to the actual schema shape (`receiver_run_id`/`sender_run_id`, per-run identity since 2026-05-15). See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`.
```

### Step 33.5 — Verify

```
grep -cF "Mark-don't-delete on drain" .ok-planner/design/concepts/wait-set.md
grep -cF 'receiver_run_id' .ok-planner/design/concepts/wait-set.md
```

Both return at least `1`.

---

## Task 34 — Update `concepts/cascade.md`

**Files:** `.ok-planner/design/concepts/cascade.md`.

### Step 34.1 — Append hard-dep walker description

In What it is or Boundaries, add: the cascade walker consults two edge maps — the subscription-edge map (existing) and the hard-dep edge map (new). Both feed the wait-set with the same row shape.

### Step 34.2 — Append Notes entry

```
- 2026-05-20 — Hard-dep edge map. The cascade walker now consults `BuildHardDepEdges` alongside `BuildSubscriptionEdges` at registration. At runtime, when invalidating a receiver R, the walker iterates R's hard-dep edges (computed from R's attribute schema fields with `hard_dep: true`); for each (R, X) hard-dep edge where X has no current-frame run, the walker proactively invalidates X via an inline stale-mark + recursive cascade walk in the same transaction, then inserts a wait-set blocker on R. See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`.
```

### Step 34.3 — Verify

```
grep -cF '2026-05-20 — Hard-dep edge map' .ok-planner/design/concepts/cascade.md
```

Returns `1`.

---

## Task 35 — Update `concepts/node-subscription.md`

**Files:** `.ok-planner/design/concepts/node-subscription.md`.

### Step 35.1 — Append a Notes entry

At the bottom of `## Notes`:

```
- 2026-05-20 — Minimalist substitution model under per-run attribute keying. Subscriptions remain push: an upstream transition causes the receiver to fire via the cascade. Attribute reads at dispatch are scoped to this-frame's contributing senders only (no scope-walk, no cross-frame caching). The auto-subscribe rule (substitution refs imply subscriptions) stays as the default and is not opt-out-able. See `concept:attribute` for the per-run keying details and the `hard_dep: true` opt-in for proactive upstream invalidation. See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`.
```

### Step 35.2 — Verify

```
grep -cF '2026-05-20 — Minimalist substitution model' .ok-planner/design/concepts/node-subscription.md
```

Returns `1`.

---

## Task 36 — Scenario test: concurrent fan-out leaves

**Files:** `test/scenarios/per_run_attributes/fanout_leaves_test.go` (new).

### Step 36.1 — Find the scenario test convention

```
ls test/scenarios/
```

Use the convention of existing directories (e.g., `test/scenarios/atomic_staging/`).

### Step 36.2 — Write the test

The test:

1. Create an instance with a fan-out parent emitting 3 leaves.
2. Each leaf's executor writes a distinct attribute (e.g., `{"value": <child_key>}`).
3. After all leaves complete, query `rimsky_node_attributes` for each leaf's `node_run_id` and assert each row contains its own distinct value.

Use existing scenario fixtures and testcontainers patterns. Read a sibling scenario test for the pattern.

### Step 36.3 — Verify

```
go test ./test/scenarios/per_run_attributes/ -count=1
```

---

## Task 37 — Scenario test: concurrent subgraph invocations + carry-rule

**Files:** `test/scenarios/per_run_attributes/subgraph_invocations_test.go` (new).

### Step 37.1 — Write the test

The test covers two intertwined properties: per-run isolation across invocations, and the carry-rule (Task 30's fix).

Template shape:

1. Parent graph contains two delegating nodes, `caller-a` and `caller-b`, both pointing at the same `delegate:` target — a subgraph S with entry node `entry`, internal node `worker`, and exit node `exit`.
2. The subgraph's `exit` writes a distinct attribute, e.g. `{"result": <something derived from a unique parameter>}`.
3. A downstream node, `consumer-a`, subscribes to `caller-a` (state) and reads `{{nodes.caller-a.attribute.result}}`. Same for `consumer-b` reading `caller-b`.

Test flow:

1. Create one instance. Trigger both `caller-a` and `caller-b` in the same instance (parameters chosen so each invocation produces a different value).
2. Wait for both invocations to complete (both subgraphs' exits fire).
3. Assert each invocation's internal-node `node_run_id` row contains the correct value (per-run isolation — Task 11's conformance test covers the persistence-layer correctness; this is the end-to-end version).
4. Assert `caller-a`'s `rimsky_node_attributes` row contains `caller-a`'s subgraph's exit writeback (the carry-rule landed for invocation A).
5. Assert `caller-b`'s `rimsky_node_attributes` row contains `caller-b`'s subgraph's exit writeback (the carry-rule landed for invocation B, and did NOT collide with A's despite sharing the same template-level node-types).
6. Assert `consumer-a`'s dispatch resolved `{{nodes.caller-a.attribute.result}}` to A's value (not B's, not absent).
7. Assert `consumer-b`'s dispatch resolved `{{nodes.caller-b.attribute.result}}` to B's value.

Steps 4–7 verify the carry-rule fix from Task 30 in concert with per-run keying. If the carry isn't persisting, steps 6/7 see `ErrMissingSource` (which the receiver's required-field gates surface as `template_resolution_failed`). If the per-run keying is wrong, A's and B's values collide on a single `node_id`-keyed row.

Use existing scenario fixtures and testcontainers patterns. Read a sibling scenario test for the pattern (`test/scenarios/atomic_staging/*_test.go` is a good model).

### Step 37.2 — Verify

```
go test ./test/scenarios/per_run_attributes/ -count=1
```

---

## Task 38 — Scenario test: Z-pattern (producer-owned recovery)

**Files:** `test/scenarios/per_run_attributes/z_pattern_test.go` (new).

### Step 38.1 — Write the test

The Z-pattern:

1. `generate-config` produces a config.
2. `verify-config` validates; on failure, emits warnings as its attribute.
3. `generate-config` subscribes back to `verify-config` on `state, when: failed, error_class: validation_failed`.
4. `generate-config`'s schema includes `{{nodes.verify-config.attribute.warnings | "No prior warnings"}}` (fallback for first dispatch).

The test:

1. First dispatch of `generate-config`: warnings absent; substitution falls through to the literal.
2. `verify-config` fails with warnings.
3. `generate-config` re-fires; reads `verify-config.attribute.warnings` (now populated via the cascade settling in the receiver's wait-set this frame).
4. Assert the second dispatch sees the warnings.

### Step 38.2 — Verify

```
go test ./test/scenarios/per_run_attributes/ -count=1
```

---

## Task 39 — Scenario test: `hard_dep` cascade

**Files:** `test/scenarios/per_run_attributes/hard_dep_test.go` (new).

### Step 39.1 — Write the test

1. Two upstreams A and B; receiver C subscribes to A only; C's schema reads `{{nodes.B.attribute.foo}}` with `hard_dep: true`.
2. A transitions. C is invalidated by A's cascade.
3. The cascade walker also invalidates B (proactive hard-dep). C's wait-set has rows for both A and B.
4. A and B both settle in the same frame.
5. C dispatches with both A's and B's current-frame attribute rows visible.
6. Assert C's substitution resolved `{{nodes.B.attribute.foo}}` from B's current-frame run.

### Step 39.2 — Verify

```
go test ./test/scenarios/per_run_attributes/ -count=1
```

---

## Task 40 — Final full build, test, lint, and CHANGELOG

**Files:** `CHANGELOG.md` (modified).

### Step 40.1 — Run the full validation suite

```
go build ./...
go test ./... -count=1
make lint
make proto-gen  # verify proto state is consistent
git diff --quiet protocols/proto/v1/gen/  # if non-empty, regen needed
```

Race-sensitive paths:

```
go test ./foundation/persistence/postgres/... ./runtime/... ./graph/scheduler/... -race -count=3
```

Scenario tests (require Docker):

```
go test ./test/scenarios/... -count=1
```

TypeScript executor:

```
cd executors/claude-agent && npm install && npm test && npm run build && cd ../..
```

### Step 40.2 — Verify no Go or TypeScript file references removed symbols

```
grep -rn "RunAttempt\b" runtime/ control/ foundation/ graph/ cmd/ conformance/ test/ protocols/
grep -rn "run_attempt" executors/claude-agent/src/
```

Should return only test files referencing Go's standard library (extremely unlikely) or historical comments. Production code references must all be gone.

### Step 40.3 — Verify all design-doc mutations landed

```
for f in attribute node-run wait-set cascade node-subscription; do
    echo "=== $f ==="
    grep -F "2026-05-20" .ok-planner/design/concepts/$f.md | head -5
done
```

Each concept file should show a 2026-05-20 Notes entry from this spec. `concepts/attribute.md` should also show the new `## Non-goals` section heading.

### Step 40.4 — Update CHANGELOG

Append a bullet under `## Unreleased` in `CHANGELOG.md`:

```
- **Per-run attribute keying.** `table:rimsky_node_attributes` re-keyed from `node_id` to `node_run_id` (per spec `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`). Completes the 2026-05-15 run-tree extension's "all state-bearing columns" intent. Substitution context at dispatch reads only drained wait-set rows for this receiver in this frame (no scope-walk, no cross-frame caching). New per-attribute `hard_dep: true` flag for opt-in proactive upstream invalidation (cascade walker consults a new `BuildHardDepEdges` map at walk time, with registration-time cycle detection). Fallback operator `{{<directive> | <literal>}}` added to substitution grammar. Five concept doc mutations: `attribute` (including a new `## Non-goals` section capturing durable design positions), `node-run`, `wait-set` (PK correction), `cascade`, `node-subscription`. Proto-breaking changes: `proto:executor.proto::ExecuteRequest.run_attempt` (field 11) and `proto:events.proto::AttributesSubstitutedPayload.run_attempt` (field 3) deleted (replaced with `reserved` directives). HTTP callback route changes from `POST /v1/attributes/{node_id}` to `POST /v1/runs/{run_id}/attributes`. TypeScript executor at `executors/claude-agent/` updated to drop `run_attempt` fields. Pre-v1 destructive.
```

### Step 40.5 — Confirm working-tree consistency

```
git status --short
```

Expected modified files (approximate):

- `foundation/persistence/postgres/migrations/003-per-run-attributes.sql` (new)
- `foundation/persistence/postgres/migrations/004-wait-set-drained-at.sql` (new)
- `foundation/persistence/sqlite/migrations/003-per-run-attributes.sql` (new)
- `foundation/persistence/sqlite/migrations/004-wait-set-drained-at.sql` (new)
- `foundation/persistence/node_attributes.go`
- `foundation/persistence/wait_set.go`
- `foundation/persistence/postgres/node_attributes.go`
- `foundation/persistence/postgres/wait_set.go`
- `foundation/persistence/postgres/nodes.go`
- `foundation/persistence/sqlite/node_attributes.go`
- `foundation/persistence/sqlite/wait_set.go`
- `foundation/persistence/sqlite/nodes.go`
- `foundation/persistence/conformance/node_attributes_merge_delta.go`
- `foundation/persistence/conformance/node_attributes_per_run.go` (new)
- `foundation/persistence/conformance/wait_set.go`
- `runtime/runner.go`
- `runtime/runner_dispatch.go`
- `runtime/runner_terminal.go`
- `runtime/runner_locks.go`
- `runtime/callback.go`
- `runtime/cascade_invalidate.go` (stalemarkAndEnqueueInFrame added)
- `runtime/subgraph_dispatch.go`
- `runtime/on_error.go`
- `runtime/sweep_parked.go`
- `runtime/subscription_loaders.go`
- `runtime/substitution_context.go` (new)
- `graph/attribute/substitution.go`
- `graph/attribute/substitution_test.go`
- `graph/attribute/callback.go`
- `graph/node/hard_dep_edges.go` (new)
- `graph/node/hard_dep_edges_test.go` (new)
- `graph/node/template_validator.go`
- `graph/node/template_validator_test.go`
- `protocols/proto/v1/executor.proto`
- `protocols/proto/v1/events.proto`
- `protocols/proto/v1/gen/*` (regenerated)
- `protocols/executor/types.go`
- `executors/claude-agent/src/http-bridge.ts`
- `executors/claude-agent/src/server.ts`
- `executors/claude-agent/src/attributes-tools.ts` (URL builder)
- `executors/claude-agent/src/attributes-tools.test.ts` (URL assertions)
- `executors/claude-agent/src/token-registry.ts` (docstring URLs)
- `executors/claude-agent/src/agent-run.ts` (URL builder call sites)
- `test/scenarios/per_run_attributes/*` (new — directory and files)
- `.ok-planner/design/concepts/attribute.md`
- `.ok-planner/design/concepts/node-run.md`
- `.ok-planner/design/concepts/wait-set.md`
- `.ok-planner/design/concepts/cascade.md`
- `.ok-planner/design/concepts/node-subscription.md`
- `CHANGELOG.md`

Confirm no unrelated files were modified.

### Step 40.6 — Confirm no Go file references removed symbols

```
git status --short | grep -E '\.go$' | xargs grep -l "force_fresh\|RunAttempt" 2>/dev/null
```

Should return empty. The `force_fresh` flag was never spec'd; only `hard_dep` lands in this implementation.

---

## Manual checks after completion

None required by the spec. All verification is automated. The user will commit the working tree when ready; no manual UI / deployment / external-environment steps.
