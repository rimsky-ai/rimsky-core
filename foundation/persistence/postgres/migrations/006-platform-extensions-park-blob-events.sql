-- 2026-05-08 Platform extensions: parked node lifecycle + blob spill +
-- named-event ledger. Bundles sections C1–C7 of
-- .ok-planner/plans/2026-05-08-platform-extensions-for-agent-consumers.md.
--
-- Pre-v1: idempotent ADD COLUMN IF NOT EXISTS / CREATE TABLE IF NOT EXISTS
-- throughout. Dev databases on the 'phase' CHECK constraint may need a
-- nuke-and-recreate; the constraint is dropped and recreated rather than
-- altered.

-- ---------------------------------------------------------------------------
-- C1. Add 'parked' to the rimsky_worker_request.phase CHECK constraint.
-- ---------------------------------------------------------------------------
ALTER TABLE rimsky_worker_request
    DROP CONSTRAINT IF EXISTS rimsky_worker_request_phase_check;
ALTER TABLE rimsky_worker_request
    ADD CONSTRAINT rimsky_worker_request_phase_check
    CHECK (phase IN ('pending','active','held','parked','completed'));

-- ---------------------------------------------------------------------------
-- C2. Park-state columns on rimsky_worker_request.
--
-- parked_at, resume_at, parked_reason, session_token: park metadata.
-- parked_payload_inline / parked_payload_handle / parked_payload_handle_backend:
--   exactly one of inline-or-handle is non-NULL when phase='parked' (write
--   path enforces; not a CHECK constraint because both NULL is also valid
--   for empty payloads).
-- ---------------------------------------------------------------------------
ALTER TABLE rimsky_worker_request
    ADD COLUMN IF NOT EXISTS parked_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS resume_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS parked_payload_inline BYTEA,
    ADD COLUMN IF NOT EXISTS parked_payload_handle TEXT,
    ADD COLUMN IF NOT EXISTS parked_payload_handle_backend TEXT,
    ADD COLUMN IF NOT EXISTS session_token TEXT,
    ADD COLUMN IF NOT EXISTS parked_reason TEXT,
    -- wake_reason carries the WakeReason enum value
    -- ("deadline_elapsed" | "external_invalidate") set by ResumeParkedInTx
    -- so the resume-dispatch path can populate ResumeContext.resume_reason
    -- accurately. NULL means "no resume in flight" (the typical state).
    ADD COLUMN IF NOT EXISTS wake_reason TEXT;

CREATE INDEX IF NOT EXISTS idx_worker_request_parked_resume
    ON rimsky_worker_request(resume_at)
    WHERE phase = 'parked' AND resume_at IS NOT NULL;

-- ---------------------------------------------------------------------------
-- C3. Blob handle columns on rimsky_node_attributes.
--
-- The existing 'data' column (JSONB, NOT NULL DEFAULT '{}'::jsonb) is the
-- inline storage. When the materialized attribute bytes exceed the
-- BlobConfig spill threshold, the write path stores the bytes in the
-- configured BlobBackend, sets value_handle to the backend-opaque
-- identifier and value_handle_backend to the backend's Name(), and
-- writes the empty object '{}' to data so the inline column stays
-- NOT-NULL-compatible with downstream tooling. The read path checks
-- value_handle first; non-NULL → fetch from the backend; NULL → use
-- the inline data column.
--
-- The pre-spill rule (write path enforces, not a CHECK constraint
-- because both paths may legitimately be empty for empty attributes):
--   * value_handle IS NOT NULL  ⇒ data = '{}'::jsonb (spill mode)
--   * value_handle IS NULL      ⇒ data carries inline bytes (legacy mode)
-- ---------------------------------------------------------------------------
ALTER TABLE rimsky_node_attributes
    ADD COLUMN IF NOT EXISTS value_handle TEXT,
    ADD COLUMN IF NOT EXISTS value_handle_backend TEXT;

-- ---------------------------------------------------------------------------
-- C4. Orphan-blob tracking. When an attribute row's value_handle is
-- overwritten (or the row is deleted), the old handle goes here with
-- reap_after = now() + retention_window. The SweepOrphanedBlobs sweep
-- deletes rows where reap_after <= now() and calls
-- BlobBackend.Delete(handle) for each.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rimsky_blob_orphans (
    handle      TEXT PRIMARY KEY,
    backend     TEXT NOT NULL,
    orphaned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reap_after  TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_blob_orphans_reap
    ON rimsky_blob_orphans(reap_after);

-- ---------------------------------------------------------------------------
-- C5. consecutive_retries_no_progress counter for the
-- max_retries_without_progress cap (E5). Reset on any last_outcome
-- change; incremented on every retry that produces no last_outcome
-- delta. When it exceeds the effective cap, the runner forces
-- Errored { error_class: "retry_loop_no_progress" } instead of retry.
-- ---------------------------------------------------------------------------
ALTER TABLE rimsky_worker_request
    ADD COLUMN IF NOT EXISTS consecutive_retries_no_progress INTEGER NOT NULL DEFAULT 0;

-- ---------------------------------------------------------------------------
-- C6. rimsky_node_events ledger. Records executor-emitted NamedEvent
-- emissions for substitution via
-- `nodes.<emitter_node>.event.<event_name>.<json_path>`.
--
-- Exactly one of payload_inline / payload_handle is non-NULL per row.
-- Substitution reads via LatestByName ordered by emitted_at DESC; the
-- ledger is append-only (no UPDATEs).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rimsky_node_events (
    id                     BIGSERIAL PRIMARY KEY,
    instance_id            UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    emitter_node_id        TEXT NOT NULL,
    event_name             TEXT NOT NULL,
    payload_inline         BYTEA,
    payload_handle         TEXT,
    payload_handle_backend TEXT,
    emitted_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    frame_id               UUID
);
CREATE INDEX IF NOT EXISTS idx_node_events_lookup
    ON rimsky_node_events(instance_id, emitter_node_id, event_name, emitted_at DESC);

-- ---------------------------------------------------------------------------
-- C7. Per-node DSL fields denormalized onto rimsky_worker_request so
-- sweeps don't need a join through templates on every tick. Both default
-- NULL meaning "use deployment default." Populated at dispatch time
-- from the resolved template DSL (max_park_duration, max_retries_without_progress).
-- ---------------------------------------------------------------------------
ALTER TABLE rimsky_worker_request
    ADD COLUMN IF NOT EXISTS max_park_duration_seconds INTEGER,
    ADD COLUMN IF NOT EXISTS max_retries_without_progress INTEGER;
