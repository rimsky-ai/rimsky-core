-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Host-agent and proxy: parallel to postgres 002. Per spec
-- 2026-05-24-host-agent-and-proxy-design.md.

-- Add the two new columns on rimsky_instances. SQLite ALTER TABLE ADD COLUMN
-- works for nullable columns with no default. JSONB → TEXT (caller marshals
-- JSON), UUID → TEXT, per the 001-schema.sql type-mapping convention.
ALTER TABLE rimsky_instances ADD COLUMN service_bindings TEXT;
ALTER TABLE rimsky_instances ADD COLUMN created_by_api_key_id TEXT REFERENCES rimsky_api_keys(id);

-- Rebuild rimsky_lifecycle_idempotencies to extend the CHECK constraints.
-- SQLite cannot DROP/ALTER a CHECK constraint in place, so use the standard
-- create-_new / copy / drop / rename idiom. The column list is copied verbatim
-- from 001-schema.sql with the two CHECKs extended:
--   scope_kind: ('template','instance','run_scope')
--   state:      ('registered','deployed','undeployed','created','run_scope_terminal')
CREATE TABLE rimsky_lifecycle_idempotencies_new (
    store_registration_name TEXT NOT NULL,
    scope_kind              TEXT NOT NULL CHECK (scope_kind IN ('template','instance','run_scope')),
    scope_id                TEXT NOT NULL,
    state                   TEXT NOT NULL CHECK (state IN ('registered','deployed','undeployed','created','run_scope_terminal')),
    last_event_at           TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (store_registration_name, scope_kind, scope_id)
);

INSERT INTO rimsky_lifecycle_idempotencies_new
    SELECT * FROM rimsky_lifecycle_idempotencies;

DROP TABLE rimsky_lifecycle_idempotencies;

ALTER TABLE rimsky_lifecycle_idempotencies_new
    RENAME TO rimsky_lifecycle_idempotencies;

-- Recreate the index 001-schema.sql declared on the table (dropped with the
-- old table).
CREATE INDEX idx_rimsky_lifecycle_idempotencies_scope
    ON rimsky_lifecycle_idempotencies (scope_kind, scope_id);
