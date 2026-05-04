-- 003-template-registry-and-lifecycle.sql
-- Control-plane v1 + store lifecycle protocol.
-- Per docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md.
--
-- Pre-v1: drop and recreate templates/instances. Existing dev databases
-- are nuked. CASCADE drops dependent rows in rimsky_nodes,
-- rimsky_dispatch, rimsky_lock_holders, rimsky_claim_holders, rimsky_frames,
-- rimsky_node_attributes, rimsky_schedules, rimsky_events.
--
-- FK accounting (audit trail for the post-DROP CASCADE re-add list).
--
-- The DROP TABLE … CASCADE on rimsky_templates / rimsky_instances drops
-- ONLY the FK constraints whose REFERENCED side was rimsky_templates or
-- rimsky_instances. Constraints whose REFERENCED side is some OTHER
-- table (rimsky_nodes, rimsky_lock_holders, rimsky_frames) survive the
-- DROP and do NOT need to be re-added.
--
-- Constraints to re-add (REFERENCED side = rimsky_templates or
-- rimsky_instances; this file's tail issues `ALTER TABLE … ADD CONSTRAINT`
-- for each):
--   rimsky_nodes.instance_id        → rimsky_instances(id)  ON DELETE CASCADE
--   rimsky_frames.instance_id       → rimsky_instances(id)  ON DELETE CASCADE
--   rimsky_events.instance_id       → rimsky_instances(id)  ON DELETE CASCADE
--
-- (rimsky_template_tags.template_id and rimsky_instances.template_hash
-- are created inline by their CREATE TABLE statements above; no
-- post-CREATE re-add is required.)
--
-- Constraints that survive (REFERENCED side ≠ rimsky_templates /
-- rimsky_instances; not re-added here):
--   rimsky_dispatch.node_id         → rimsky_nodes(id)
--   rimsky_node_attributes.node_id  → rimsky_nodes(id)
--   rimsky_schedules.node_id        → rimsky_nodes(id)
--   rimsky_lock_holders.holder_node_id → rimsky_nodes(id)
--   rimsky_claim_holders.holder_node_id → rimsky_nodes(id)
--   rimsky_events.node_id           → rimsky_nodes(id)
--   rimsky_dispatch.frame_id        → rimsky_frames(frame_id)
--   rimsky_nodes.frame_id           → rimsky_frames(frame_id)
--   rimsky_claim_holders.lock_holder_id → rimsky_lock_holders(id)

DROP TABLE IF EXISTS rimsky_instances CASCADE;
DROP TABLE IF EXISTS rimsky_templates CASCADE;

CREATE TABLE rimsky_templates (
    id              TEXT        PRIMARY KEY,
    spec            JSONB       NOT NULL,
    state           TEXT        NOT NULL CHECK (state IN ('registered','deployed','undeployed')),
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    source          TEXT        NOT NULL DEFAULT 'direct'
);

CREATE TABLE rimsky_template_tags (
    tag             TEXT        PRIMARY KEY,
    template_id     TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rimsky_template_tags_template_id ON rimsky_template_tags(template_id);

CREATE TABLE rimsky_instances (
    id             UUID        PRIMARY KEY,
    template_hash  TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key   TEXT,
    params         JSONB       NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- terminated_at is the implementation choice for spec §10 open question 1
    -- (instance terminal-event detection mechanism). Set by the scheduler/frame
    -- terminal-predicate evaluation; polled by the control-api terminator
    -- worker which fires OnInstanceTerminated.
    terminated_at  TIMESTAMPTZ,
    UNIQUE (template_hash, instance_key)
);

CREATE INDEX idx_rimsky_instances_terminated
    ON rimsky_instances (terminated_at)
    WHERE terminated_at IS NOT NULL;

CREATE TABLE rimsky_lifecycle_idempotency (
    store_registration_name TEXT        NOT NULL,
    scope_kind              TEXT        NOT NULL CHECK (scope_kind IN ('template','instance')),
    scope_id                TEXT        NOT NULL,
    state                   TEXT        NOT NULL CHECK (state IN ('registered','deployed','undeployed','created')),
    last_event_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (store_registration_name, scope_kind, scope_id)
);

CREATE INDEX idx_rimsky_lifecycle_idempotency_scope
    ON rimsky_lifecycle_idempotency (scope_kind, scope_id);

-- Re-establish FK constraints from existing tables to the recreated
-- rimsky_instances. Postgres' DROP TABLE … CASCADE removes the FK
-- constraint but leaves the dependent table and its rows alone; if a
-- dev DB had rows in these tables they need to be cleared (pre-v1
-- nuke), but the FKs must come back so future inserts validate.
--
-- TRUNCATE … RESTART IDENTITY CASCADE clears all dependent rows in a
-- single statement, automatically following the FK graph in the right
-- order (so we don't have to maintain an ordered DELETE list as the
-- schema grows).
TRUNCATE TABLE rimsky_events,
               rimsky_dispatch,
               rimsky_node_attributes,
               rimsky_schedules,
               rimsky_claim_holders,
               rimsky_lock_holders,
               rimsky_frames,
               rimsky_nodes
    RESTART IDENTITY CASCADE;

ALTER TABLE rimsky_nodes
    ADD CONSTRAINT rimsky_nodes_instance_id_fkey
    FOREIGN KEY (instance_id) REFERENCES rimsky_instances(id) ON DELETE CASCADE;

ALTER TABLE rimsky_frames
    ADD CONSTRAINT rimsky_frames_instance_id_fkey
    FOREIGN KEY (instance_id) REFERENCES rimsky_instances(id) ON DELETE CASCADE;

ALTER TABLE rimsky_events
    ADD CONSTRAINT rimsky_events_instance_id_fkey
    FOREIGN KEY (instance_id) REFERENCES rimsky_instances(id) ON DELETE CASCADE;
