# Frame Resolution Implementation Plan

**Goal:** Implement the frame-resolution semantics from `docs/specs/2026-04-26-frame-resolution-design.md` end-to-end: per-template `frame_resolution: coalesce | serial_queue` mode, scheduler-owned frame engine, schema additions (`rimsky_frames` table + `frame_id` columns), removal of `kill_requested` and its kill-poll path, full scenario+unit test coverage, and the smoke fixture passing under serial_queue.

**Architecture:** New `core/frame/` package owns the producer helper (`EnqueueOrCoalesce`) and the scheduler-tick frame-engine logic (frame-end detection, queued→running advancement, stuck-frame reaper). Schema migration 002 adds the `rimsky_frames` table and `frame_id` columns on `rimsky_dispatch`, `rimsky_nodes`, `rimsky_lock_holders`, `rimsky_claim_holders`, while dropping `rimsky_nodes.kill_requested`. Producers (schedule_ticker, controlapi nodes/invalidate route, admin force-fire indirectly) call `frame.EnqueueOrCoalesce`; the supervisor propagates `frame_id` through claims, terminal commits, and cascade message-passes. Five new blessed invariants (15-19) are added.

**Tech Stack:** Go 1.22+ (single root module `github.com/fallguy/rimsky`), Postgres 15 (testcontainers-go), pgx/v5, log/slog, chi, robfig/cron/v3, go-yaml. TS executor unchanged.

**Foundation:** `docs/specs/2026-04-25-stores-redesign-design.md` is the active foundation spec. References to §X.Y in this plan are to the frame-resolution spec at `docs/specs/2026-04-26-frame-resolution-design.md` unless prefixed `stores-redesign §X.Y`.

**No commits.** The plan produces working-tree edits and verification commands. The user commits when ready.

---

## File map (new + modified)

**New:**
- `core/migrations/002-frame-resolution.sql`
- `core/frame/doc.go`
- `core/frame/types.go` — `Mode`, `State`, `Frame` struct
- `core/frame/producer.go` — `EnqueueOrCoalesce`
- `core/frame/producer_test.go`
- `core/frame/engine.go` — `RunTick(ctx, tx)` invoked from scheduler
- `core/frame/engine_test.go`
- `test/scenarios/frame_resolution/` — 13 scenario tests (one file per test)

**Modified (Go):**
- `core/migrations/embed.go` — pick up the new SQL file (if not via globbing)
- `core/node/template.go`, `core/node/template_validator.go` (+ `_test.go`) — add `FrameResolution`, `FrameTimeoutMs`
- `core/controlapi/templates.go` — propagate validation errors
- `core/controlapi/nodes.go` — invalidate route calls `frame.EnqueueOrCoalesce`; remove kill_requested write
- `core/controlapi/admin_force_fire.go` — no logic change; comment update
- `core/storage/postgres/nodes.go` — drop `kill_requested` column reads/writes; add `frame_id` reads/writes
- `core/storage/postgres/claim_holders.go` — `frame_id` observability column
- `core/storage/postgres/schedules.go` — read template's `frame_resolution`+`frame_timeout_ms` for the ticker (or extend the join)
- `core/queue/postgres/queue.go` (+ `_test.go`) — add `frame_id` to dispatch reads/writes; new index helpers
- `core/scheduler/scheduler.go` — invoke `frame.RunTick` inside the existing tick advisory-lock window
- `core/scheduler/schedule_ticker.go` — call `frame.EnqueueOrCoalesce` instead of marking nodes stale directly
- `core/scheduler/sweep_locks.go` — leave as-is unless touched by holder-helper changes
- `core/supervisor/runner.go`, `runner_acquire.go`, `runner_dispatch.go`, `runner_terminal.go`, `runner_held_claims.go`, `runner_locks.go` — propagate `frame_id`; remove `isKillRequested` and the kill-poll branch
- `core/supervisor/supervisor.go` — remove the heartbeat-tick `kill_requested` poll
- `core/supervisor/callback.go` — doc note
- `core/store/lockholders.go` — `frame_id` observability column on insert
- `core/store/claimstorepg/holders.go` — `frame_id` observability column on insert

**Modified (Docs/data):**
- `CLAUDE.md` — gotchas update
- `docs/architecture.md`, `docs/operator-guide.md`, `docs/node-graph-design.md`
- `CHANGELOG.md` — `## Unreleased` bullet
- `test/smoke/fixtures/template.yml` — add `frame_resolution: serial_queue`

---

## Tasks

Each task lists files, numbered atomic steps with code where useful, and verification commands. **Every verification is a command the executing agent can run and interpret.** Tests assert specific numbers/columns; the agent reads pass/fail from `go test` output.

---

### Task 1 — Write the SQL migration

**Files:** `core/migrations/002-frame-resolution.sql` (new)

**Steps:**

1. Create `core/migrations/002-frame-resolution.sql` with the full migration body:

```sql
-- 002-frame-resolution.sql
-- Frame-resolution semantics per docs/specs/2026-04-26-frame-resolution-design.md.
-- Adds the rimsky_frames table and frame_id columns; drops rimsky_nodes.kill_requested.
-- Pre-v1: in-flight cascades are abandoned by transitioning stale/running nodes to failed
-- (see migration step 6).

CREATE TABLE IF NOT EXISTS rimsky_frames (
    frame_id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id       UUID         NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    mode              TEXT         NOT NULL CHECK (mode IN ('coalesce','serial_queue')),
    state             TEXT         NOT NULL CHECK (state IN ('queued','running','completed','failed')),
    source_node_ids   UUID[]       NOT NULL CHECK (array_length(source_node_ids, 1) >= 1),
    queued_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    started_at        TIMESTAMPTZ,
    ended_at          TIMESTAMPTZ,
    frame_timeout_ms  BIGINT       NOT NULL CHECK (frame_timeout_ms >= 60000),
    CONSTRAINT chk_running_has_started CHECK (state != 'running' OR started_at IS NOT NULL),
    CONSTRAINT chk_terminal_has_ended  CHECK (state NOT IN ('completed','failed') OR ended_at IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_rimsky_frames_queued
    ON rimsky_frames (instance_id, queued_at)
    WHERE state = 'queued';

CREATE UNIQUE INDEX IF NOT EXISTS uq_rimsky_frames_running
    ON rimsky_frames (instance_id)
    WHERE state = 'running';

CREATE UNIQUE INDEX IF NOT EXISTS uq_rimsky_frames_coalesce_queued
    ON rimsky_frames (instance_id)
    WHERE state = 'queued' AND mode = 'coalesce';

-- Abandon any in-flight cascade rows BEFORE the schema change so frame_id NOT NULL is satisfiable.
UPDATE rimsky_nodes SET state = 'failed' WHERE state IN ('stale','running');
DELETE FROM rimsky_dispatch;  -- best-effort; no frame_id retroactively assignable

-- rimsky_dispatch: add frame_id (NOT NULL after the DELETE above means the table is empty post-step-7).
ALTER TABLE rimsky_dispatch
    ADD COLUMN frame_id UUID NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_rimsky_dispatch_frame
    ON rimsky_dispatch (frame_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_dispatch_frame_claimed
    ON rimsky_dispatch (frame_id) WHERE claimed_by IS NOT NULL;

-- rimsky_nodes: drop kill_requested; add frame_id.
ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS kill_requested;
ALTER TABLE rimsky_nodes
    ADD COLUMN frame_id UUID REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_rimsky_nodes_frame_state
    ON rimsky_nodes (frame_id, state)
    WHERE state IN ('stale','running');

-- rimsky_lock_holders: observability frame_id.
ALTER TABLE rimsky_lock_holders ADD COLUMN frame_id UUID;

-- rimsky_claim_holders: observability frame_id.
ALTER TABLE rimsky_claim_holders ADD COLUMN frame_id UUID;
```

2. Read `core/migrations/embed.go` to confirm whether migrations are embedded by file glob or by explicit list. If by explicit list, append `"002-frame-resolution.sql"` to the list.

**Verification:**

```sh
ls core/migrations/002-frame-resolution.sql && grep -c "rimsky_frames\|kill_requested" core/migrations/002-frame-resolution.sql
# expect file present and match count >= 5
```

---

### Task 2 — Apply migration in a fresh testcontainer Postgres and confirm shape

**Files:** uses existing `core/internal/pgtest` harness + a one-off ad-hoc check.

**Steps:**

1. Write a temporary Go test at `core/migrations/runner_test.go` (or extend the existing one if there is one) that:
   - Boots the pgtest container.
   - Runs the embedded migrations (the existing migration runner).
   - Queries `information_schema.tables` for `rimsky_frames`, `information_schema.columns` for `rimsky_dispatch.frame_id`, `rimsky_nodes.frame_id`, `rimsky_lock_holders.frame_id`, `rimsky_claim_holders.frame_id`, and `rimsky_nodes.kill_requested`.
   - Asserts `rimsky_frames` exists, all four `frame_id` columns exist, and `rimsky_nodes.kill_requested` does NOT exist.
   - Queries the `pg_indexes` view for `uq_rimsky_frames_running`, `uq_rimsky_frames_coalesce_queued`, `idx_rimsky_frames_queued`, `idx_rimsky_dispatch_frame`, `idx_rimsky_dispatch_frame_claimed`, `idx_rimsky_nodes_frame_state` — all five must be present.

2. If `core/migrations/runner_test.go` already exists, add a new top-level test function `TestMigration002FrameResolutionSchema(t *testing.T)`.

**Verification:**

```sh
go test ./core/migrations/... -run TestMigration002FrameResolutionSchema -count=1 -v
# expect PASS
```

---

### Task 3 — Add `FrameResolution` and `FrameTimeoutMs` fields to the template type

**Files:** `core/node/template.go`

**Steps:**

1. Open `core/node/template.go` and find the top-level template struct (likely `Template` or `TemplateDef`).
2. Add two fields:
   ```go
   FrameResolution string `yaml:"frame_resolution" json:"frame_resolution"`
   FrameTimeoutMs  int64  `yaml:"frame_timeout_ms,omitempty" json:"frame_timeout_ms,omitempty"`
   ```
3. Define exported constants in the same file:
   ```go
   const (
       FrameResolutionCoalesce    = "coalesce"
       FrameResolutionSerialQueue = "serial_queue"
       FrameTimeoutDefaultMs      = int64(600000) // 10 minutes
       FrameTimeoutMinMs          = int64(60000)  // 60 seconds (hard floor)
   )
   ```

**Verification:**

```sh
go build ./core/node/...
# expect no errors
```

---

### Task 4 — Validate `frame_resolution` and `frame_timeout_ms` in template_validator.go

**Files:** `core/node/template_validator.go`, `core/node/template_validator_test.go`

**Steps:**

1. Open `core/node/template_validator.go` and find the entry-point validation function (likely `ValidateTemplate` or similar).
2. Add validation after the existing top-level-field checks:
   ```go
   switch t.FrameResolution {
   case FrameResolutionCoalesce, FrameResolutionSerialQueue:
       // ok
   case "":
       return fmt.Errorf("template %q: frame_resolution is required (one of: %q, %q)",
           t.Name, FrameResolutionCoalesce, FrameResolutionSerialQueue)
   default:
       return fmt.Errorf("template %q: frame_resolution = %q is not a valid value (one of: %q, %q)",
           t.Name, t.FrameResolution, FrameResolutionCoalesce, FrameResolutionSerialQueue)
   }

   if t.FrameTimeoutMs == 0 {
       t.FrameTimeoutMs = FrameTimeoutDefaultMs
   } else if t.FrameTimeoutMs < FrameTimeoutMinMs {
       return fmt.Errorf("template %q: frame_timeout_ms = %d is below hard floor %d",
           t.Name, t.FrameTimeoutMs, FrameTimeoutMinMs)
   }
   ```
3. Open `core/node/template_validator_test.go` and add three table-driven test cases:
   - Missing `frame_resolution` → returns error mentioning the field.
   - Invalid value `"abort"` → returns error mentioning the field.
   - Valid `frame_resolution: "serial_queue"` with no `frame_timeout_ms` → no error AND post-validation the struct's `FrameTimeoutMs == 600000`.
   - `frame_timeout_ms: 30000` → returns error mentioning the floor.

**Verification:**

```sh
go test ./core/node/... -run TestTemplate -count=1 -v
# expect PASS for all four cases
```

---

### Task 5 — Add controlapi error formatting for frame-validation failures

**Files:** `core/controlapi/templates.go`

**Steps:**

1. Open `core/controlapi/templates.go` and find the template-upload route handler (POST `/v1/templates`).
2. Confirm the existing error-response shape for validation failures (likely `app.WriteError(w, r, http.StatusBadRequest, err)` or similar). The validation error from Task 4 surfaces through this path automatically — no new logic needed if the existing path already wraps validator errors as 400.
3. If the existing path drops error messages, ensure the validator error is included in the response body.

**Verification:**

```sh
go test ./core/controlapi/... -count=1
# expect PASS (no regression)
```

---

### Task 6 — Define the frame package types

**Files:** `core/frame/doc.go` (new), `core/frame/types.go` (new)

**Steps:**

1. Create `core/frame/doc.go`:
   ```go
   // Package frame implements the frame-resolution engine per
   // docs/specs/2026-04-26-frame-resolution-design.md.
   //
   // The producer helper (EnqueueOrCoalesce) is called by schedule_ticker,
   // controlapi/nodes invalidate route, and any other source of an
   // invalidation event. The engine (RunTick) is called by the scheduler
   // tick under the existing pg_try_advisory_lock(SCHEDULER_TICK_KEY).
   //
   // Frames are per-instance. Mode is per-template (coalesce | serial_queue).
   // Under both modes frames execute one at a time per instance — at most
   // one rimsky_frames row in 'running' state per instance, enforced by
   // uq_rimsky_frames_running.
   package frame
   ```

2. Create `core/frame/types.go`:
   ```go
   package frame

   import (
       "time"

       "github.com/google/uuid"
   )

   type Mode string

   const (
       ModeCoalesce    Mode = "coalesce"
       ModeSerialQueue Mode = "serial_queue"
   )

   type State string

   const (
       StateQueued    State = "queued"
       StateRunning   State = "running"
       StateCompleted State = "completed"
       StateFailed    State = "failed"
   )

   type Frame struct {
       ID             uuid.UUID
       InstanceID     uuid.UUID
       Mode           Mode
       State          State
       SourceNodeIDs  []uuid.UUID
       QueuedAt       time.Time
       StartedAt      *time.Time
       EndedAt        *time.Time
       FrameTimeoutMs int64
   }
   ```

**Verification:**

```sh
go build ./core/frame/...
# expect no errors
```

---

### Task 7 — Implement `EnqueueOrCoalesce`

**Files:** `core/frame/producer.go` (new)

**Steps:**

1. Create `core/frame/producer.go` with the producer helper:

```go
package frame

import (
    "context"
    "errors"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
)

// EnqueueOrCoalesce inserts (serial_queue) or upserts (coalesce) a queued
// frame for the instance. The caller passes a tx so the enqueue can join
// the producer's existing transaction (e.g., the schedule_ticker's tick tx,
// or the controlapi handler's request tx).
//
// Returns the frame_id of the row that received the source — either a
// freshly-created row or an existing pending-coalesce row.
//
// @blessed-invariant 15 (mode mandatory): the helper reads mode from the
// template join and rejects if missing.
func EnqueueOrCoalesce(ctx context.Context, tx pgx.Tx,
    instanceID, sourceNodeID uuid.UUID) (uuid.UUID, error) {

    var (
        mode           string
        frameTimeoutMs int64
    )
    // Template config is stored as JSONB in rimsky_templates.spec.
    // Validation (Task 4) guarantees non-empty frame_resolution; default
    // frame_timeout_ms when missing/zero.
    err := tx.QueryRow(ctx, `
        SELECT t.spec->>'frame_resolution' AS mode,
               COALESCE(NULLIF((t.spec->>'frame_timeout_ms'),'')::bigint, 600000) AS frame_timeout_ms
        FROM rimsky_instances i
        JOIN rimsky_templates  t ON t.id = i.template_id
        WHERE i.id = $1
    `, instanceID).Scan(&mode, &frameTimeoutMs)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce: instance %s not found", instanceID)
        }
        return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce: lookup template: %w", err)
    }
    if frameTimeoutMs <= 0 {
        frameTimeoutMs = 600000
    }

    switch Mode(mode) {
    case ModeSerialQueue:
        return enqueueSerial(ctx, tx, instanceID, sourceNodeID, frameTimeoutMs)
    case ModeCoalesce:
        return enqueueCoalesce(ctx, tx, instanceID, sourceNodeID, frameTimeoutMs)
    default:
        return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce: unsupported mode %q for instance %s",
            mode, instanceID)
    }
}

func enqueueSerial(ctx context.Context, tx pgx.Tx,
    instanceID, sourceNodeID uuid.UUID, timeoutMs int64) (uuid.UUID, error) {

    var frameID uuid.UUID
    err := tx.QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, mode, state, source_node_ids, queued_at, frame_timeout_ms)
        VALUES ($1, 'serial_queue', 'queued', ARRAY[$2]::UUID[], now(), $3)
        RETURNING frame_id
    `, instanceID, sourceNodeID, timeoutMs).Scan(&frameID)
    if err != nil {
        return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce(serial_queue): insert: %w", err)
    }
    return frameID, nil
}

func enqueueCoalesce(ctx context.Context, tx pgx.Tx,
    instanceID, sourceNodeID uuid.UUID, timeoutMs int64) (uuid.UUID, error) {

    // Try to append to an existing queued coalesce row.
    var frameID uuid.UUID
    err := tx.QueryRow(ctx, `
        UPDATE rimsky_frames
        SET source_node_ids = (
            CASE WHEN $2 = ANY(source_node_ids) THEN source_node_ids
                 ELSE array_append(source_node_ids, $2)
            END
        )
        WHERE instance_id = $1
          AND state = 'queued'
          AND mode = 'coalesce'
        RETURNING frame_id
    `, instanceID, sourceNodeID).Scan(&frameID)
    if err == nil {
        return frameID, nil
    }
    if !errors.Is(err, pgx.ErrNoRows) {
        return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce(coalesce): update: %w", err)
    }

    // No existing queued row — insert a new one.
    err = tx.QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, mode, state, source_node_ids, queued_at, frame_timeout_ms)
        VALUES ($1, 'coalesce', 'queued', ARRAY[$2]::UUID[], now(), $3)
        RETURNING frame_id
    `, instanceID, sourceNodeID, timeoutMs).Scan(&frameID)
    if err != nil {
        return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce(coalesce): insert: %w", err)
    }
    return frameID, nil
}
```

**Verification:**

```sh
go build ./core/frame/...
# expect no errors
```

---

### Task 8 — Unit tests for `EnqueueOrCoalesce`

**Files:** `core/frame/producer_test.go` (new)

**Steps:**

1. Create `core/frame/producer_test.go` with table-driven tests using the existing `core/internal/pgtest` harness:
   - **serial_queue branch:** seed an instance with template `frame_resolution=serial_queue`. Call `EnqueueOrCoalesce` 3 times. Assert 3 rows in `rimsky_frames`, all `state='queued'`, all `mode='serial_queue'`, `source_node_ids` length 1 each.
   - **coalesce branch — no running, no queued:** seed instance with `frame_resolution=coalesce`. Call once. Assert 1 row, `state='queued'`, `mode='coalesce'`.
   - **coalesce branch — append to existing queued:** seed coalesce instance with one queued coalesce row. Call `EnqueueOrCoalesce` with two different source IDs. Assert still 1 row, `source_node_ids` contains both.
   - **coalesce branch — dedupe same source:** call `EnqueueOrCoalesce` with the same source twice. Assert 1 row, `source_node_ids` length 1.
   - **invalid template mode:** seed an instance whose template has `frame_resolution=''`. Call `EnqueueOrCoalesce`. Assert error mentioning "unsupported mode".
   - **instance not found:** call with a random UUID. Assert error mentioning "not found".

**Verification:**

```sh
go test ./core/frame/... -run TestEnqueueOrCoalesce -count=1 -v
# expect PASS for all 6 cases
```

---

### Task 9 — Implement the frame engine `RunTick`

**Files:** `core/frame/engine.go` (new)

**Steps:**

1. Create `core/frame/engine.go`:

```go
package frame

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
)

// RunTick performs one frame-engine iteration. The caller must hold the
// scheduler-tick advisory lock (blessed-invariant 7).
//
// Steps per §4.1 of the spec:
//   1. Detect frame-end (transition running → completed|failed).
//   2. Advance queue (serial_queue) — promote oldest queued to running.
//   3. Advance trailing (coalesce) — same, gated on mode.
//   4. Reap stuck frames.
//   5. Reap orphan dispatches (frame in terminal state but dispatch still claimed).
func RunTick(ctx context.Context, db *pgxPool, logger *slog.Logger) error {
    // Implementation note: each step opens its own short tx so partial failures
    // don't poison the whole tick. The advisory lock guarantees serialization
    // across replicas; within one process this is just a sequential loop.

    if err := runFrameEndDetection(ctx, db, logger); err != nil {
        return fmt.Errorf("frame.RunTick: frame-end: %w", err)
    }
    if err := runAdvanceQueued(ctx, db, logger); err != nil {
        return fmt.Errorf("frame.RunTick: advance: %w", err)
    }
    if err := runReapStuckFrames(ctx, db, logger); err != nil {
        return fmt.Errorf("frame.RunTick: reap stuck: %w", err)
    }
    if err := runReapOrphanFrameDispatches(ctx, db, logger); err != nil {
        return fmt.Errorf("frame.RunTick: reap orphan: %w", err)
    }
    return nil
}

// pgxPool is the minimum interface RunTick needs from a pgx.Pool.
// Defined locally to avoid importing the full pgxpool.Pool surface in tests.
type pgxPool interface {
    BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

func runFrameEndDetection(ctx context.Context, db pgxPool, logger *slog.Logger) error {
    tx, err := db.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx) //nolint:errcheck

    // For each running frame, check the frame-end predicate (§4.2).
    rows, err := tx.Query(ctx, `
        SELECT f.frame_id, f.instance_id
        FROM rimsky_frames f
        WHERE f.state = 'running'
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_nodes n
              WHERE n.instance_id = f.instance_id
                AND n.state IN ('stale','running')
          )
    `)
    if err != nil {
        return err
    }
    type pending struct {
        frameID    uuid.UUID
        instanceID uuid.UUID
    }
    var pendings []pending
    for rows.Next() {
        var p pending
        if err := rows.Scan(&p.frameID, &p.instanceID); err != nil {
            rows.Close()
            return err
        }
        pendings = append(pendings, p)
    }
    rows.Close()

    for _, p := range pendings {
        // Determine completed vs failed by joining dispatch outcomes for this frame.
        var anyFailed bool
        if err := tx.QueryRow(ctx, `
            SELECT EXISTS (
                SELECT 1 FROM rimsky_nodes n
                WHERE n.instance_id = $1
                  AND n.frame_id = $2
                  AND n.state = 'failed'
            )
        `, p.instanceID, p.frameID).Scan(&anyFailed); err != nil {
            return err
        }

        finalState := StateCompleted
        if anyFailed {
            finalState = StateFailed
        }

        cmd, err := tx.Exec(ctx, `
            UPDATE rimsky_frames
            SET state = $1, ended_at = now()
            WHERE frame_id = $2 AND state = 'running'
        `, finalState, p.frameID)
        if err != nil {
            return err
        }
        if cmd.RowsAffected() == 1 {
            logger.Info("frame.end",
                "frame_id", p.frameID,
                "instance_id", p.instanceID,
                "final_state", finalState)
        }
    }
    return tx.Commit(ctx)
}

func runAdvanceQueued(ctx context.Context, db pgxPool, logger *slog.Logger) error {
    tx, err := db.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx) //nolint:errcheck

    // Find instances with no running frame and at least one queued frame; pick the oldest.
    rows, err := tx.Query(ctx, `
        SELECT DISTINCT ON (f.instance_id)
            f.frame_id, f.instance_id, f.source_node_ids
        FROM rimsky_frames f
        WHERE f.state = 'queued'
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_frames r
              WHERE r.instance_id = f.instance_id AND r.state = 'running'
          )
        ORDER BY f.instance_id, f.queued_at ASC
    `)
    if err != nil {
        return err
    }
    type advance struct {
        frameID    uuid.UUID
        instanceID uuid.UUID
        sources    []uuid.UUID
    }
    var advances []advance
    for rows.Next() {
        var a advance
        if err := rows.Scan(&a.frameID, &a.instanceID, &a.sources); err != nil {
            rows.Close()
            return err
        }
        advances = append(advances, a)
    }
    rows.Close()

    for _, a := range advances {
        // CAS the frame to running.
        cmd, err := tx.Exec(ctx, `
            UPDATE rimsky_frames
            SET state = 'running', started_at = now()
            WHERE frame_id = $1 AND state = 'queued'
        `, a.frameID)
        if err != nil {
            return err
        }
        if cmd.RowsAffected() != 1 {
            // Another replica won; skip.
            continue
        }
        // Set source nodes stale with frame_id.
        for _, src := range a.sources {
            cmd, err := tx.Exec(ctx, `
                UPDATE rimsky_nodes
                SET state = 'stale', frame_id = $1, updated_at = now()
                WHERE instance_id = $2 AND id = $3
                  AND state IN ('fresh','failed')
            `, a.frameID, a.instanceID, src)
            if err != nil {
                return err
            }
            if cmd.RowsAffected() != 1 {
                logger.Warn("frame.start.source_not_in_bounds",
                    "frame_id", a.frameID,
                    "instance_id", a.instanceID,
                    "source_node_id", src)
                // Tx rollback: deferred.
                return errors.New("frame.RunTick: source node not in fresh|failed at frame-start")
            }
        }
        logger.Info("frame.start",
            "frame_id", a.frameID,
            "instance_id", a.instanceID,
            "source_node_ids", a.sources)
    }
    return tx.Commit(ctx)
}

func runReapStuckFrames(ctx context.Context, db pgxPool, logger *slog.Logger) error {
    tx, err := db.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx) //nolint:errcheck

    rows, err := tx.Query(ctx, `
        SELECT f.frame_id, f.instance_id, f.frame_timeout_ms
        FROM rimsky_frames f
        WHERE f.state = 'running'
          AND f.started_at + (f.frame_timeout_ms || ' milliseconds')::interval < now()
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_dispatch d
              WHERE d.frame_id = f.frame_id AND d.claimed_by IS NOT NULL
          )
          AND EXISTS (
              SELECT 1 FROM rimsky_nodes n
              WHERE n.instance_id = f.instance_id AND n.state IN ('stale','running')
          )
    `)
    if err != nil {
        return err
    }
    type stuck struct {
        frameID    uuid.UUID
        instanceID uuid.UUID
        timeout    int64
    }
    var stuckFrames []stuck
    for rows.Next() {
        var s stuck
        if err := rows.Scan(&s.frameID, &s.instanceID, &s.timeout); err != nil {
            rows.Close()
            return err
        }
        stuckFrames = append(stuckFrames, s)
    }
    rows.Close()

    for _, s := range stuckFrames {
        if _, err := tx.Exec(ctx, `
            UPDATE rimsky_nodes
            SET state = 'failed', updated_at = now()
            WHERE instance_id = $1 AND state IN ('stale','running')
        `, s.instanceID); err != nil {
            return err
        }
        if _, err := tx.Exec(ctx, `
            UPDATE rimsky_frames SET state = 'failed', ended_at = now()
            WHERE frame_id = $1 AND state = 'running'
        `, s.frameID); err != nil {
            return err
        }
        logger.Warn("frame.stuck.reaped",
            "frame_id", s.frameID,
            "instance_id", s.instanceID,
            "timeout_ms", s.timeout)
    }
    return tx.Commit(ctx)
}

func runReapOrphanFrameDispatches(ctx context.Context, db pgxPool, logger *slog.Logger) error {
    tx, err := db.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx) //nolint:errcheck

    cmd, err := tx.Exec(ctx, `
        UPDATE rimsky_dispatch d
        SET claimed_by = NULL, claimed_at = NULL, last_heartbeat_at = NULL
        FROM rimsky_frames f
        WHERE d.frame_id = f.frame_id
          AND d.claimed_by IS NOT NULL
          AND f.state IN ('completed','failed')
    `)
    if err != nil {
        return err
    }
    if n := cmd.RowsAffected(); n > 0 {
        logger.Warn("frame.orphan_dispatch.reaped", "count", n)
    }
    _ = time.Now() // keep import in case of future use
    return tx.Commit(ctx)
}
```

2. The `pgxPool` interface is intentionally minimal so tests can pass a stub or the real `*pgxpool.Pool`. The scheduler will pass its existing pool.

**Verification:**

```sh
go build ./core/frame/...
# expect no errors
```

---

### Task 10 — Engine unit tests via real Postgres

**Files:** `core/frame/engine_test.go` (new)

**Steps:**

1. Create `core/frame/engine_test.go` with table-driven scenario coverage of `RunTick`:
   - **Frame-end detection.** Seed instance + template + a single running frame + nodes all in `fresh`. Call `RunTick`. Assert frame transitions to `completed`. Repeat with one node `failed` for this frame_id; assert frame transitions to `failed`.
   - **Advance queued (serial_queue).** Seed two queued frames for the same instance. Call `RunTick`. Assert exactly one transitions to running; assert source-node states transition to `stale`.
   - **Advance trailing (coalesce).** Seed running frame + queued coalesce row. Trigger frame-end (mark all nodes fresh). Call `RunTick` twice (first ends, second starts). Assert coalesce frame is now `running`.
   - **Reap stuck frame.** Seed running frame with `started_at = now() - 11 minutes` (default 10-min timeout exceeded), one wedged `stale` node, no claimed dispatches. Call `RunTick`. Assert frame `failed`, wedged node `failed`.
   - **Reap orphan dispatch.** Seed completed frame with a stranded dispatch row (`claimed_by = some_supervisor`). Call `RunTick`. Assert dispatch's `claimed_by` is now NULL.
   - **No-op when nothing to do.** Empty DB + RunTick → no error, no DB change.

**Verification:**

```sh
go test ./core/frame/... -run TestRunTick -count=1 -v
# expect PASS for all 6 cases
```

---

### Task 11 — Wire `frame.RunTick` into the scheduler tick

**Files:** `core/scheduler/scheduler.go`

**Steps:**

1. Open `core/scheduler/scheduler.go` and find the existing tick loop (the body that runs under `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`).
2. Inside the held-lock section, add a call to `frame.RunTick(ctx, s.db, s.logger)` after the existing scheduler operations (orphan reap, schedule firing, etc.) but before releasing the lock.
3. Add the `core/frame` import at the top.
4. If `s.db` is a `*pgxpool.Pool`, pass it directly — `RunTick`'s `pgxPool` interface is satisfied by `BeginTx`.

**Verification:**

```sh
go build ./core/scheduler/...
go test ./core/scheduler/... -count=1
# expect PASS / no compile errors
```

---

### Task 12 — Convert the invalidate path to call `EnqueueOrCoalesce`

**Files:** `core/scheduler/invalidate.go`, `core/scheduler/schedule_ticker.go`, `core/scheduler/invalidate_test.go`, `core/scheduler/schedule_ticker_test.go`

**Steps:**

1. Open `core/scheduler/invalidate.go`. The function `InvalidateNode` (or similar) is the single canonical entry point for "this node should be invalidated" — schedule_ticker, controlapi, and any other producer all flow through it. Locate the section that currently sets `rimsky_nodes.state = 'stale'` (likely via the storage interface, e.g., `sb.Nodes().SetState(...)`). Around line 107 also locate `sb.Nodes().SetKillRequested(ctx, target.ID, true, nil)` — this is the kill-request flag write that this spec deletes (covered in detail in Task 13).
2. Replace the direct state-stale write with a call to `frame.EnqueueOrCoalesce(ctx, tx, target.InstanceID, target.ID)` inside whatever tx is already open in this code path. Add the `core/frame` import.
3. **Remove** the `SetKillRequested` call entirely (do not pass it through; it has no replacement under the frame model — operator invalidates do not preempt).
4. Open `core/scheduler/schedule_ticker.go`. Confirm the schedule_ticker fires by calling into `InvalidateNode` (or its message-dispatcher equivalent). If schedule_ticker currently writes its own `state='stale'`, redirect it through `InvalidateNode`. (If it already calls `InvalidateNode` it gets the new behaviour for free.)
5. Update `core/scheduler/invalidate_test.go` and `core/scheduler/schedule_ticker_test.go`: any test that asserted the direct state-stale write or the kill_requested flag should now assert a `rimsky_frames` row was inserted with the appropriate source_node_id.

**Verification:**

```sh
go test ./core/scheduler/... -run "TestInvalidate|TestScheduleTicker" -count=1 -v
# expect PASS
```

---

### Task 13 — Remove the controlapi `/nodes/{id}/kill` route and `KillRequested` API field

**Files:** `core/controlapi/nodes.go`, `core/controlapi/admin_routes_test.go`

**Steps:**

1. Open `core/controlapi/nodes.go`.
2. **Remove the kill route registration** at the line that currently looks like:
   ```go
   r.Post("/nodes/{id}/kill", handleKillNode)
   ```
   (around line 78).
3. **Remove the `handleKillNode` handler** (lines ~190-225) entirely. It calls `Storage.Nodes().SetKillRequested(...)` which is being deleted (Task 16).
4. **Remove the `KillRequested` field** from the `nodeResponse` JSON struct (line 37) and its setter in the response constructor (line 60).
5. The invalidate route (POST `/v1/nodes/{node_id}/invalidate`) — if it currently calls `SetKillRequested` directly (rather than going through `scheduler.InvalidateNode`) — remove that call. If it goes through `InvalidateNode`, the upstream removal in Task 12 covers this; just confirm.
6. Open `core/controlapi/admin_routes_test.go`. Remove the `TestKillNode` test (or whatever the kill-route's test is named). Remove any `KillRequested` field assertions in node-list/get tests.

**Verification:**

```sh
go build ./core/controlapi/...
grep -rn "KillRequested\|/kill\b\|handleKillNode" core/controlapi/
# expect no matches in non-test files
go test ./core/controlapi/... -count=1
# expect PASS
```

---

### Task 14 — Update controlapi invalidate-route test to assert frame insert

**Files:** `core/controlapi/admin_routes_test.go` (the existing test file that exercises the invalidate route — confirmed via codebase search; alternative is `app_test.go` if the route is tested there)

**Steps:**

1. Locate the existing test for the invalidate route in `admin_routes_test.go` (or `app_test.go` if not present in the former).
2. Update it (or add a new assertion) to confirm that after `POST /v1/nodes/{node_id}/invalidate` returns 204:
   - A `rimsky_frames` row exists for the node's instance.
   - The row's `source_node_ids` contains the invalidated node.
   - For a serial_queue template: state is `queued`. For a coalesce template: state is `queued` (still — first-invalidate latency).
3. Confirm `kill_requested` no longer appears anywhere in this test file's assertions or seeds (Task 13 + Task 16 handle the removal; this is a defensive check).

**Verification:**

```sh
go test ./core/controlapi/... -count=1 -v
# expect PASS
```

---

### Task 15 — Remove `KillRequested` field + `SetKillRequested` from storage interface and impl

**Files:** `core/storage/interfaces.go`, `core/storage/postgres/nodes.go`, `core/storage/postgres/postgres_test.go`

**Steps:**

1. Open `core/storage/interfaces.go`:
   - Remove the `KillRequested bool` field from the `NodeRow` struct (around line 104).
   - Remove the `SetKillRequested(...)` method declaration from the `Nodes` interface (around line 131).
   - Add `FrameID *shared.UUID` to the `NodeRow` struct (nullable; mirrors the column nullability in §10.3).
2. Open `core/storage/postgres/nodes.go`:
   - Delete the `SetKillRequested` method implementation (around line 341).
   - In the `kill_requested` column reads (around line 380's `Scan(...)`): remove `&r.KillRequested` from the scan target list.
   - In SELECT column lists: remove `kill_requested`. Add `frame_id`.
   - In INSERT column lists / VALUES (e.g., when creating a node): remove `kill_requested`. Add `frame_id` (insert as NULL on creation; nodes start `fresh` with no frame).
   - In `ListReadyForDispatch` (around line 133) — confirmed needed for Task 18: include `frame_id` in the returned row data so the dispatch enqueue path can propagate it.
3. Add a setter helper to `core/storage/postgres/nodes.go` (also exposed via the `Nodes` interface in `interfaces.go`):
   ```go
   func (s *NodeStore) SetFrameID(ctx context.Context, id shared.UUID, frameID *shared.UUID, tx storage.Tx) error {
       q := `UPDATE rimsky_nodes SET frame_id = $1, updated_at = now() WHERE id = $2`
       if tx != nil {
           _, err := tx.(*pgxTx).inner.Exec(ctx, q, frameID, id)
           return err
       }
       _, err := s.pool.Exec(ctx, q, frameID, id)
       return err
   }
   ```
   (Adjust to match the existing `Tx` casting pattern in this file.)
4. Open `core/storage/postgres/postgres_test.go`. Remove every reference to `KillRequested` / `kill_requested`. Add `FrameID` assertions where the test inspects node rows.

**Verification:**

```sh
go build ./core/storage/...
grep -rn "KillRequested\|kill_requested\|SetKillRequested" core/storage/
# expect no matches
go test ./core/storage/... -count=1
# expect PASS
```

---

### Task 16 — Remove `isKillRequested` and the supervisor's kill-poll path

**Files:** `core/supervisor/runner_dispatch.go`, `core/supervisor/supervisor.go`, supervisor `*_test.go` files that touch kill semantics

**Steps:**

1. Open `core/supervisor/runner_dispatch.go`. Find `isKillRequested` and its caller in the executor stream-recv loop. Delete the function and remove the caller branch (the executor-stream `select` case that polls it).
2. Open `core/supervisor/supervisor.go`. Find the heartbeat-tick code that polls `rimsky_nodes.kill_requested` and signals cancel/SIGTERM (the heartbeat case in `runLoop`, per stores-redesign §13.4). Remove that polling and the corresponding cancel-token signalling. The heartbeat tick now only updates `last_heartbeat_at` for the supervisor's running nodes.
3. Update package-doc comments in `supervisor.go` and `runner_dispatch.go` to remove every mention of `kill_requested`, `isKillRequested`, kill-poll, and operator-driven preemption. Replace with one-liner: "Operator invalidates enqueue/coalesce frames; in-flight work is never preempted."
4. Scan supervisor test files (`runner_test.go`, `supervisor_test.go`, `commit_test.go`, `on_error_test.go`, `callback_test.go`) for any test that asserts kill_requested behaviour — delete or rewrite to match the no-preemption model.

**Verification:**

```sh
grep -rn "kill_requested\|isKillRequested\|KillRequested" core/supervisor/
# expect zero matches
go build ./core/supervisor/...
go test ./core/supervisor/... -count=1
# expect PASS
```

---

### Task 17 — Update `controlapi/admin_routes_test.go` to remove kill_requested assertions

**Files:** `core/controlapi/admin_routes_test.go`

**Steps:**

1. Find every reference to `kill_requested` in this file.
2. For tests that asserted "operator-prefixed reason sets kill_requested=true", change them to assert "operator-prefixed reason enqueues a frame; no preemption."
3. For tests that simply seeded `kill_requested`, remove the seeding.

**Verification:**

```sh
go test ./core/controlapi/... -count=1
# expect PASS
```

---

### Task 18 — Propagate `frame_id` through the dispatch enqueue chain

**Files:** `core/queue/types.go` (or wherever `queue.DispatchRequest` is defined), `core/queue/postgres/queue.go`, `core/queue/postgres/queue_test.go`, `core/scheduler/scheduler.go`

The dispatch insert path under stores-redesign is: `sweepReady` (`scheduler.go:361-380`) reads ready-stale nodes via `Storage.Nodes().ListReadyForDispatch`, builds a `queue.DispatchRequest`, calls `cfg.Queue.Enqueue` → `core/queue/postgres/queue.go:63 Queue.Enqueue` which writes the `rimsky_dispatch` row. There is also a second enqueue site at `scheduler.go:305` (likely the recalculate path; covered too). The frame_id must flow from `rimsky_nodes.frame_id` through this chain to `rimsky_dispatch.frame_id`.

**Steps:**

1. Open `core/queue/types.go` (or the file that defines `DispatchRequest` — find via `grep -n "type DispatchRequest" core/queue/`). Add a field:
   ```go
   FrameID shared.UUID  // required; sourced from rimsky_nodes.frame_id at enqueue time
   ```
2. Open `core/queue/postgres/queue.go:63 Queue.Enqueue`. Update the INSERT SQL to include `frame_id` in the column list and `$N` placeholder; pass `req.FrameID`.
3. Confirm the query that reads dispatch rows (e.g., `SELECT … FROM rimsky_dispatch …` for orphan reap, candidate selection, etc.) — add `frame_id` to the column list and the row scan target. The dispatch struct gains `FrameID shared.UUID`.
4. Open `core/storage/postgres/nodes.go::ListReadyForDispatch`. The returned `NodeRow` already carries `FrameID` after Task 15. Confirm the SQL SELECT in this method includes `frame_id` from `rimsky_nodes`.
5. Open `core/scheduler/scheduler.go`:
   - At line ~305 and line ~369, both `cfg.Queue.Enqueue(ctx, queue.DispatchRequest{...})` calls construct a `DispatchRequest`. Add `FrameID: row.FrameID` to each (where `row` is the `NodeRow` source). If `row.FrameID` is nil at this point (which would be a bug — the node should not be `stale` without a frame), log a warning and skip the enqueue (defensive; mirrors the supervisor's frame_id_null check in Task 21).
6. Update `core/queue/postgres/queue_test.go`: any test that constructs a `DispatchRequest` must now seed a `rimsky_frames` row first and set `FrameID`. Add a small helper:
   ```go
   func seedFrame(t *testing.T, ctx context.Context, pool *pgxpool.Pool, instanceID shared.UUID) shared.UUID {
       var id shared.UUID
       err := pool.QueryRow(ctx, `
           INSERT INTO rimsky_frames(instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
           VALUES ($1, 'serial_queue', 'running', ARRAY[gen_random_uuid()]::UUID[], now(), now(), 600000)
           RETURNING frame_id
       `, instanceID).Scan(&id)
       require.NoError(t, err)
       return id
   }
   ```

**Verification:**

```sh
go build ./...
go test ./core/queue/postgres/... ./core/scheduler/... -count=1
# expect PASS
```

---

### Task 19 — Propagate `frame_id` through supervisor dispatch claim

**Files:** `core/supervisor/runner_acquire.go`, `core/supervisor/runner.go`

**Steps:**

1. Open `core/supervisor/runner_acquire.go`. Find the dispatch-row SELECT (the candidate read in §13.3 step 1 of stores-redesign).
2. Ensure it reads `frame_id`. The struct used to carry the candidate (likely in `runner.go` or a sibling) gains `FrameID uuid.UUID`.
3. The acquisition tx (which inserts `rimsky_lock_holders` rows) passes `frame_id` to the lock-holder insert (Task 22).
4. The verify-before-run check stays as-is; it doesn't need frame_id.

**Verification:**

```sh
go build ./core/supervisor/...
# expect no errors
```

---

### Task 20 — Propagate `frame_id` on cascade message-pass at terminal commit

**Files:** `core/supervisor/runner_terminal.go`, `core/supervisor/commit_test.go`

**Steps:**

1. Open `core/supervisor/runner_terminal.go`. Find the cascade-message-pass logic (the part that marks children stale on successful commit). It is co-located with the terminal-commit transaction.
2. Read the parent node's `frame_id` (the dispatch row already carries it; just propagate). Update the children-stale write:
   ```go
   _, err := tx.Exec(ctx, `
       UPDATE rimsky_nodes
       SET state = 'stale', frame_id = $1, updated_at = now()
       WHERE instance_id = $2 AND id = $3 AND state = 'fresh'
   `, parentFrameID, instanceID, childID)
   ```
3. On terminal commit (`completed`): the parent's `rimsky_nodes.frame_id` is cleared (set NULL) in the same tx — `UPDATE rimsky_nodes SET state='fresh', frame_id=NULL WHERE id = $parentID`.
4. On terminal failure (`failed`): the parent's `frame_id` is preserved — `UPDATE rimsky_nodes SET state='failed' WHERE id = $parentID` (do NOT touch frame_id).
5. **Dispatch enqueue for the child:** if the child becomes stale and the supervisor's commit path enqueues the child's dispatch row directly (rather than waiting for the next scheduler tick's `sweepReady`), the dispatch insert must carry `frame_id = parentFrameID`. Locate the enqueue call (likely `cfg.Queue.Enqueue(...)` or similar) and add `FrameID: parentFrameID` to the `DispatchRequest`. If the supervisor commit path defers child-enqueue to the next scheduler tick, no change here — the scheduler's `sweepReady` (Task 18) reads `frame_id` from the child's `rimsky_nodes` row and propagates it.
6. Update `core/supervisor/commit_test.go` to assert: after terminal commit of a parent in frame F, the child's `rimsky_nodes.frame_id = F` AND its dispatch row has `frame_id = F`.

**Verification:**

```sh
go test ./core/supervisor/... -run "TestRunner|TestCommit" -count=1 -v
# expect PASS
```

---

### Task 21 — Guard against NULL `frame_id` on claim

**Files:** `core/supervisor/runner_acquire.go`

**Steps:**

1. After the candidate SELECT, add a defensive check: if `dispatch.FrameID == uuid.Nil`, log a structured warning with `dispatch_id`, `node_id` and bail with reason `frame_id_null` (do not claim).
2. This satisfies blessed-invariant 19's no-NULL-frame-id rule.

**Verification:**

```sh
go test ./core/supervisor/... -count=1
# expect PASS
```

---

### Task 22 — Add `frame_id` observability column to `core/store/lockholders.go`

**Files:** `core/store/lockholders.go`

**Steps:**

1. Open `core/store/lockholders.go`. Find the `rimsky_lock_holders` INSERT.
2. Add `frame_id` to the column list and `$N` placeholder.
3. The `LockHolder` struct (or equivalent) gains `FrameID *uuid.UUID` (nullable per §10.4).
4. Read paths and DELETE/release paths do not change — `frame_id` is observability-only.
5. The supervisor's acquire path (Task 19) passes the dispatch's `frame_id` to the lock-holder insert.

**Verification:**

```sh
go build ./core/store/...
# expect no errors
```

---

### Task 23 — Add `frame_id` observability column to `core/store/claimstorepg/holders.go`

**Files:** `core/store/claimstorepg/holders.go`

**Steps:**

1. Open `core/store/claimstorepg/holders.go`. Find the `rimsky_claim_holders` INSERT (where claim holders are inserted at commit of the claiming-source node).
2. Add `frame_id` to the column list. The supervisor's commit path passes the dispatch's `frame_id`.
3. Read paths and the §5.6.4 resolution algorithm do NOT change — `frame_id` is observability.

**Verification:**

```sh
go build ./core/store/claimstorepg/...
go test ./core/store/claimstorepg/... -count=1
# expect PASS
```

---

### Task 24 — Update existing supervisor scenario tests for frame_id propagation

**Files:** `core/supervisor/*_test.go`, `core/supervisor/commit_test.go`

**Steps:**

1. Find supervisor tests that seed dispatch rows directly (without going through `frame.EnqueueOrCoalesce`). These tests will fail because the migration makes `rimsky_dispatch.frame_id` NOT NULL.
2. Update each to seed a `rimsky_frames` row first and pass its `frame_id` to the dispatch seed.
3. Add a small helper (e.g., in `core/supervisor/testing_helpers.go` if such exists, or inline) `seedRunningFrame(t, db, instanceID, sourceNodeID) uuid.UUID` that inserts a running frame row and returns its id.
4. Test files to scan: `runner_test.go`, `commit_test.go`, `callback_test.go`, `on_error_test.go`, `supervisor_test.go`.

**Verification:**

```sh
go test ./core/supervisor/... -count=1
# expect PASS
```

---

### Task 25 — Update existing scenario tests in `test/scenarios/`

**Files:** `test/scenarios/*.go` (everything that touches dispatch or kill_requested)

**Steps:**

1. Run `grep -rn "kill_requested\|rimsky_dispatch" test/scenarios/` to find every site.
2. For dispatch seeds: add a frame row first, pass its frame_id.
3. For kill_requested-driven tests: rework or remove, because in-flight preemption no longer exists. The scenario `verify_before_run_race_test.go` and the locks/claim_stores subdir tests need careful pass: they likely don't depend on kill semantics, only on dispatch shape.
4. Add a `seedRunningFrame` helper in `test/scenarios/` (or extend the existing test fixture helper) for consistency.

**Verification:**

```sh
go test ./test/scenarios/... -count=1
# expect PASS
```

---

### Task 26 — Scenario test: `serial_queue_each_invalidate_one_frame_test.go`

**Files:** `test/scenarios/frame_resolution/serial_queue_each_invalidate_one_frame_test.go` (new)

**Steps:**

1. Boot pgtest container, run migrations, seed one template (`frame_resolution: serial_queue`), one instance, one source node.
2. Call `frame.EnqueueOrCoalesce` 10 times in quick succession (simulating producer fires) inside its own tx each.
3. Assert `SELECT count(*) FROM rimsky_frames WHERE instance_id = $1 AND state = 'queued' = 10`.
4. Drive `frame.RunTick` repeatedly with a stub executor that immediately succeeds each dispatch (fast path); after sufficient ticks, assert all 10 frames `state = 'completed'`. Use the in-process scheduler from existing test harness if available.
5. Assert `SELECT count(*) FROM rimsky_dispatch WHERE …` indicates 10 distinct dispatches (one per frame's source).

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestSerialQueueEachInvalidateOneFrame -count=1 -v
# expect PASS
```

---

### Task 27 — Scenario test: `coalesce_collapses_invalidates_test.go`

**Files:** `test/scenarios/frame_resolution/coalesce_collapses_invalidates_test.go` (new)

**Steps:**

1. Seed coalesce template + instance + source node.
2. Call `frame.EnqueueOrCoalesce` once (becomes the queued first frame).
3. Drive `RunTick` once to advance it to `running`.
4. While it's running (don't drive a stub commit yet), call `EnqueueOrCoalesce` 9 more times with varying source_node_ids.
5. Assert: 1 row in `state='running'`; 1 row in `state='queued'`; queued row's `source_node_ids` length = number of distinct sources used.
6. Drive a stub commit on the running frame's source. RunTick → frame ends, queued advances to running.
7. Assert at most 2 frames ever existed in this instance; second carries the union source set.

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestCoalesceCollapsesInvalidates -count=1 -v
# expect PASS
```

---

### Task 28 — Scenario test: `frame_in_flight_blocks_next_serial_queue_test.go`

**Files:** `test/scenarios/frame_resolution/frame_in_flight_blocks_next_serial_queue_test.go` (new)

**Steps:**

1. Seed serial_queue template+instance+source.
2. Enqueue + advance to running, but do NOT commit (use a stub executor that blocks).
3. Enqueue again. Assert second row stays `queued` while first is `running`.
4. Unblock the stub executor; commit succeeds; RunTick detects frame-end; advances queued→running.
5. Assert serialization: only one row at a time in `running` (poll over the test duration).

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestFrameInFlightBlocksNextSerialQueue -count=1 -v
# expect PASS
```

---

### Task 29 — Scenario test: `frame_in_flight_pending_coalesce_test.go`

**Files:** `test/scenarios/frame_resolution/frame_in_flight_pending_coalesce_test.go` (new)

**Steps:**

1. Seed coalesce template+instance.
2. Start one running frame (via Enqueue + RunTick).
3. Call EnqueueOrCoalesce 5 more times concurrently from goroutines using independent transactions.
4. After all 5 calls return, assert exactly 1 `queued` row exists (per the partial unique index `uq_rimsky_frames_coalesce_queued`).
5. Assert no goroutine returned a unique-violation error (the helper's UPDATE-then-INSERT path absorbs concurrent writes safely; if not, document the race and fail).

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestFrameInFlightPendingCoalesce -count=1 -race -v
# expect PASS
```

---

### Task 30 — Scenario test: `frame_end_after_async_callback_test.go`

**Files:** `test/scenarios/frame_resolution/frame_end_after_async_callback_test.go` (new)

**Steps:**

1. Seed serial_queue template with two nodes — source and one downstream.
2. Source executor returns `AsyncAccepted` (use the async-handoff fake from existing async tests, e.g., `core/supervisor/callback_test.go`'s harness).
3. Start frame via Enqueue + RunTick. Assert frame remains `running`; source is `running`; dispatch has `claimed_by IS NOT NULL`.
4. Issue a callback POST `/v1/callback/{async_ack_id}` with terminal-completed payload.
5. Assert the source's dispatch resolves; cascade flows to downstream; downstream commits; RunTick → frame `completed`.
6. Assert `rimsky_dispatch.frame_id` matches between the source dispatch, the callback resolution path, and the downstream dispatch.

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestFrameEndAfterAsyncCallback -count=1 -v
# expect PASS
```

---

### Task 31 — Scenario test: `frame_timeout_reaper_test.go`

**Files:** `test/scenarios/frame_resolution/frame_timeout_reaper_test.go` (new)

**Steps:**

1. Seed template with `frame_timeout_ms = 60000` (minimum).
2. Insert a running frame manually with `started_at = now() - 2 minutes`. Insert a `stale` rimsky_nodes row with this `frame_id`. NO claimed dispatches.
3. Call `frame.RunTick`. Assert: frame transitions to `failed`; the wedged stale node transitions to `failed`; an event-log row exists for the reap.

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestFrameTimeoutReaper -count=1 -v
# expect PASS
```

---

### Task 32 — Scenario test: `pruned_node_does_not_block_frame_end_test.go`

**Files:** `test/scenarios/frame_resolution/pruned_node_does_not_block_frame_end_test.go` (new)

**Steps:**

1. Seed serial_queue template with three nodes: source → middle → leaf.
2. Configure middle's executor to commit `changed: false`.
3. Enqueue + run frame to completion.
4. Assert: source has dispatch row; middle has dispatch row; leaf has NO dispatch row for this frame_id.
5. Assert frame `completed`.

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestPrunedNodeDoesNotBlockFrameEnd -count=1 -v
# expect PASS
```

---

### Task 33 — Scenario test: `held_claim_resolution_at_frame_end_test.go`

**Files:** `test/scenarios/frame_resolution/held_claim_resolution_at_frame_end_test.go` (new)

**Steps:**

1. Seed template with claim-store: source acquires + holds an item, leaf resolves it (per stores-redesign §11.4 / §5.6.4).
2. Run frame end-to-end.
3. Assert `rimsky_claim_holders.frame_id` matches the frame's id.
4. Assert resolution happens at the leaf's commit (assert the claim-holder row's `state='completed'` and `actual_action` is set BEFORE `frame.RunTick` runs frame-end detection).
5. Assert frame ends `completed`.

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestHeldClaimResolutionAtFrameEnd -count=1 -v
# expect PASS
```

---

### Task 34 — Scenario test: `failed_node_marks_frame_failed_test.go`

**Files:** `test/scenarios/frame_resolution/failed_node_marks_frame_failed_test.go` (new)

**Steps:**

1. Seed template with one node whose executor errors.
2. Enqueue, run.
3. Assert node's `rimsky_nodes.state = 'failed'` AND `frame_id` is preserved (matches the frame).
4. Assert frame ends `failed`.
5. Enqueue a second frame (new invalidate); assert it advances to running on next RunTick.

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestFailedNodeMarksFrameFailed -count=1 -v
# expect PASS
```

---

### Task 35 — Scenario test: `template_missing_frame_resolution_rejected_test.go`

**Files:** `test/scenarios/frame_resolution/template_missing_frame_resolution_rejected_test.go` (new)

**Steps:**

1. Boot the controlapi process (in-process via existing test harness).
2. POST a template YAML missing `frame_resolution`. Assert HTTP 400; response mentions `frame_resolution`.
3. POST with `frame_resolution: "abort"`. Assert HTTP 400; response mentions valid values.
4. POST with `frame_resolution: "serial_queue"` and `frame_timeout_ms: 30000`. Assert HTTP 400; response mentions floor.
5. POST with `frame_resolution: "serial_queue"` only. Assert HTTP 201/200 (success).

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestTemplateMissingFrameResolutionRejected -count=1 -v
# expect PASS
```

---

### Task 36 — Scenario test: `per_instance_ordering_invariant_test.go` (blessed invariant 16)

**Files:** `test/scenarios/frame_resolution/per_instance_ordering_invariant_test.go` (new)

**Steps:**

1. Seed instance.
2. Insert a `running` `rimsky_frames` row directly via SQL.
3. Attempt a second `running` insert via SQL. Assert unique-violation error from the partial index `uq_rimsky_frames_running`.
4. Repeat with concurrent goroutines that race on `EnqueueOrCoalesce` → `RunTick`-driven advancement; poll for at most one running row at any moment over the test wall-clock.

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestPerInstanceOrderingInvariant -count=1 -v
# expect PASS
```

---

### Task 37 — Scenario test: `frame_start_atomicity_test.go` (blessed invariant 18)

**Files:** `test/scenarios/frame_resolution/frame_start_atomicity_test.go` (new)

**Steps:**

1. Seed two queued frames in different instances (so they don't conflict).
2. Run two goroutines that each call `frame.RunTick` against the same DB.
3. Both goroutines may advance their own instance's queued frame; assert each advanced exactly one frame.
4. For one specific instance with one queued frame, run two parallel `RunTick`s; assert exactly one CAS succeeds (one frame advanced once). The other tx rolls back.
5. After successful frame-start: assert `rimsky_frames.state='running' AND started_at IS NOT NULL` AND every source_node has `rimsky_nodes.state='stale' AND frame_id = <expected>` — visible from a single SELECT after both goroutines return.

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestFrameStartAtomicity -count=1 -race -v
# expect PASS
```

---

### Task 38 — Scenario test: `no_null_frame_id_on_in_flight_dispatch_test.go` (blessed invariant 19)

**Files:** `test/scenarios/frame_resolution/no_null_frame_id_on_in_flight_dispatch_test.go` (new)

**Steps:**

1. Drive a multi-node frame to a mid-flight state (source committed, downstream dispatch enqueued and claimed).
2. Assert `SELECT count(*) FROM rimsky_dispatch WHERE frame_id IS NULL = 0`.
3. Assert `SELECT count(*) FROM rimsky_nodes WHERE state IN ('stale','running') AND frame_id IS NULL = 0`.
4. After the frame completes, `rimsky_nodes.frame_id` for the now-fresh nodes is NULL — assert this too.
5. After the frame completes, `rimsky_dispatch.frame_id` is preserved (audit) — assert any retained dispatch rows still have non-NULL `frame_id`.

**Verification:**

```sh
go test ./test/scenarios/frame_resolution/... -run TestNoNullFrameIDOnInFlightDispatch -count=1 -v
# expect PASS
```

---

### Task 39 — Update smoke fixture template

**Files:** `test/smoke/fixtures/template.yml`

**Steps:**

1. Open `test/smoke/fixtures/template.yml`.
2. Add at the top level:
   ```yaml
   frame_resolution: serial_queue
   frame_timeout_ms: 600000
   ```

**Verification:**

```sh
grep -c "frame_resolution: serial_queue" test/smoke/fixtures/template.yml
# expect 1
```

---

### Task 40 — Run the smoke fixture end-to-end

**Files:** none modified; verification only.

**Steps:**

1. Build the docker images (they require Postgres). The existing smoke harness handles boot.
2. Run `go test ./test/smoke/... -count=1 -v -timeout=10m`.
3. Read the test output for the §19.2 acceptance predicate (≥100 terminal commits over 100 force-fires).

**Verification:**

```sh
go test ./test/smoke/... -count=1 -v -timeout=10m
# expect PASS, with assertion >= 100 terminal commits
```

---

### Task 41 — Update CHANGELOG

**Files:** `CHANGELOG.md`

**Steps:**

1. Open `CHANGELOG.md`. Find the `## Unreleased` section (or create at the top if absent).
2. Append:
   ```markdown
   - Frame resolution (per `docs/specs/2026-04-26-frame-resolution-design.md`): templates declare `frame_resolution: coalesce | serial_queue` (required) and optional `frame_timeout_ms` (default 600000, floor 60000); `rimsky_frames` table tracks frame queue/state per instance; scheduler tick owns frame-end detection, queue advancement, and stuck-frame reap; `kill_requested` and the supervisor kill-poll path are removed (operator invalidates enqueue/coalesce a frame, never preempt). Scenario tests in `test/scenarios/frame_resolution/`. Smoke fixture passes under serial_queue. Migration 002.
   ```

**Verification:**

```sh
grep -c "frame_resolution" CHANGELOG.md
# expect >= 1
```

---

### Task 42 — Update CLAUDE.md gotchas

**Files:** `CLAUDE.md`

**Steps:**

1. Open `CLAUDE.md`. Find the gotcha "Operator-originated invalidates set kill_requested=true". Replace its body with:
   ```markdown
   - **Operator-originated invalidates enqueue or coalesce a frame.** They do NOT preempt running work. The `kill_requested` column on `rimsky_nodes` is gone. See `docs/specs/2026-04-26-frame-resolution-design.md`.
   ```
2. Add a new gotcha (or extend "Where to look first"):
   ```markdown
   - **Frames are the unit of cascade resolution.** Every `rimsky_dispatch` row carries `frame_id`; every non-fresh `rimsky_nodes` row carries `frame_id`. Frame-end is detected by the scheduler tick when `rimsky_nodes.state IN ('stale','running')` is empty for the instance. Templates declare `frame_resolution: coalesce | serial_queue` (required).
   ```
3. Update the "blessed invariants" section: append invariants 15-19 to the numbered list (text from §18 of the spec).
4. Update the "Where to look first" list to add `docs/specs/2026-04-26-frame-resolution-design.md`.

**Verification:**

```sh
grep -c "frame_id\|frame_resolution\|EnqueueOrCoalesce" CLAUDE.md
# expect >= 4
grep "kill_requested" CLAUDE.md
# expect no matches (or only inside historical text describing the removal)
```

---

### Task 43 — Update `docs/architecture.md`

**Files:** `docs/architecture.md`

**Steps:**

1. Add a new section "Frame resolution" describing:
   - The frame primitive (per-instance, per-mode).
   - Scheduler ownership of the engine.
   - The `rimsky_frames` table and `frame_id` columns.
   - Removal of `kill_requested`.
2. Update the "blessed invariants" section if it lists them: append 15-19.
3. Reference the spec at `docs/specs/2026-04-26-frame-resolution-design.md`.

**Verification:**

```sh
grep -c "frame_resolution\|frame_id\|rimsky_frames" docs/architecture.md
# expect >= 3
```

---

### Task 44 — Update `docs/operator-guide.md`

**Files:** `docs/operator-guide.md`

**Steps:**

1. Add a section "Frame resolution and templates" describing:
   - The `frame_resolution` required template field.
   - The `frame_timeout_ms` optional field (default 600000, floor 60000).
   - How to query frame state: `SELECT * FROM rimsky_frames WHERE instance_id = …`.
   - How operator invalidates work post-removal of kill_requested.
   - How to read frame-completion events from `rimsky_events`.
2. Remove any prior section that described `kill_requested`-driven cancellation.

**Verification:**

```sh
grep -c "frame_resolution\|frame_timeout_ms" docs/operator-guide.md
# expect >= 2
```

---

### Task 45 — Update `docs/node-graph-design.md`

**Files:** `docs/node-graph-design.md`

**Steps:**

1. Add a section "Frames as the unit of resolution" describing the conceptual model from spec §1-§2 (the implicit invariant in reactive node graphs and how rimsky's frame primitive enforces it). Keep terse.
2. Cross-reference the spec.

**Verification:**

```sh
grep -c "frame" docs/node-graph-design.md
# expect >= 5
```

---

### Task 45.5 — Update `docs/protocol.md` with the supervisor-internal `frame_id` note

**Files:** `docs/protocol.md`

**Steps:**

1. Open `docs/protocol.md`. Confirm there is no wire-protocol change in this redesign (executors do not see `frame_id`).
2. Add a one-paragraph note (in the "out of band" / "supervisor-internal state" section, or at the doc's end if no such section exists) explaining: "The supervisor associates each dispatch with a `frame_id` (per `docs/specs/2026-04-26-frame-resolution-design.md`); this identifier is supervisor-internal and is not transmitted in the executor protocol. Executors do not need to be aware of frames."

**Verification:**

```sh
grep -c "frame_id\|frame-resolution" docs/protocol.md
# expect >= 1
```

---

### Task 46 — Update `core/supervisor/callback.go` doc comment

**Files:** `core/supervisor/callback.go`

**Steps:**

1. Open the file. In the package or function doc comment for the callback handler, add a sentence:
   ```
   // The dispatch row's frame_id is preserved across async handoff;
   // the callback resolution path commits cascade message-passes that
   // inherit the parent's frame_id (see core/supervisor/runner_terminal.go).
   ```
2. No logic change.

**Verification:**

```sh
go build ./core/supervisor/...
# expect no errors
```

---

### Task 47 — Confirm `core/controlapi/admin_force_fire.go` is unchanged

**Files:** `core/controlapi/admin_force_fire.go`

**Steps:**

1. Open the file. Confirm the handler runs `UPDATE rimsky_schedules SET next_fire_at = now() WHERE node_id = $1` and returns 204.
2. Add a doc comment if absent:
   ```go
   // Force-fire updates the schedule row's next_fire_at; the next scheduler
   // tick picks it up and calls frame.EnqueueOrCoalesce via schedule_ticker.
   // No direct frame-engine call from this handler.
   ```

**Verification:**

```sh
grep -c "frame.EnqueueOrCoalesce" core/controlapi/admin_force_fire.go
# expect at least the doc-comment mention
go build ./core/controlapi/...
```

---

### Task 48 — Final full build + lint sweep

**Files:** none modified; verification only.

**Steps:**

1. Run `go build ./...`.
2. Run `make lint`.
3. Run `make tidy` if go.mod changes occurred (e.g., new dependency on `github.com/google/uuid` if it isn't already imported by these packages).

**Verification:**

```sh
go build ./... && make lint
# expect zero errors / zero lint findings (or only pre-existing warnings; new code clean)
```

---

### Task 49 — Full test suite + race-mode sweep

**Files:** none modified; verification only.

**Steps:**

1. `go test ./... -count=1`. All unit + scenario tests must pass.
2. `go test ./core/scheduler/... ./core/supervisor/... ./core/frame/... ./test/scenarios/frame_resolution/... -race -count=3`. Race-mode runs 3x to surface flakes.
3. If any test flakes, debug root cause and fix before continuing.

**Verification:**

```sh
go test ./... -count=1
# expect PASS

go test ./core/scheduler/... ./core/supervisor/... ./core/frame/... ./test/scenarios/frame_resolution/... -race -count=3
# expect PASS
```

---

### Task 50 — Reference deployment smoke

**Files:** none modified; verification only.

**Steps:**

1. `docker compose -f deploy/docker-compose.yml up -d` (assumes Docker daemon available — same prerequisite as testcontainers).
2. Wait for `curl -fsS http://localhost:8080/health` to return 200 (use `until curl -fsS … ; do sleep 2 ; done` if needed; cap at 60s).
3. POST a template with `frame_resolution: serial_queue` to `http://localhost:8080/v1/templates`. Confirm 200/201.
4. POST a template missing `frame_resolution`. Confirm 400.
5. `docker compose -f deploy/docker-compose.yml down`.

**Verification:**

```sh
docker compose -f deploy/docker-compose.yml up -d
until curl -fsS http://localhost:8080/health; do sleep 2; done

# Submit a valid template via curl
curl -fsS -X POST http://localhost:8080/v1/templates \
  -H "content-type: application/yaml" \
  --data "$(cat <<'YAML'
name: smoke_template
frame_resolution: serial_queue
frame_timeout_ms: 600000
nodes: []
YAML
)"

# Submit an invalid template (missing frame_resolution)
curl -i -X POST http://localhost:8080/v1/templates \
  -H "content-type: application/yaml" \
  --data 'name: bad_template
nodes: []' 2>&1 | grep -E "HTTP/.*400"

docker compose -f deploy/docker-compose.yml down
```

---

## Manual checks after completion

Run after the agent reports done. None of these block the automated run; they are post-implementation review surfaces.

1. **Read `docs/specs/2026-04-26-frame-resolution-design.md` end-to-end** alongside the implementation to confirm the spec was followed.
2. **Inspect `rimsky_frames` row evolution under load** — boot the smoke fixture, watch frames flow queued → running → completed in real time. Confirm the trace matches the mental model.
3. **Helm chart sanity** (out of scope for this plan; CLAUDE.md notes it is stale). Check whether anything in `deploy/kubernetes/rimsky-chart/` references `kill_requested` and update separately if so.
4. **Decide ship-sequencing relative to stores-redesign.** The stores-redesign work is uncommitted in the working tree alongside this; the user owns the commit/merge decision.
5. **Read the implementation notes file** (`docs/plans/2026-04-26-frame-resolution-notes.md`) the executing agent will produce. Walk each entry; decide what to act on.
