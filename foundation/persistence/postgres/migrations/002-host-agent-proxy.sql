-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Host-agent and proxy: per-spec 2026-05-24-host-agent-and-proxy-design.md.
--
-- Adds two new columns on rimsky_instances + extends the CHECK constraint
-- vocabulary on rimsky_lifecycle_idempotencies for the new "run_scope"
-- scope_kind and "run_scope_terminal" state value.

-- Add the two new columns.
--   service_bindings      — opaque JSONB carrying the per-instance late-bound
--                           service catalog. NULL for instances that don't use
--                           late-bound services.
--   created_by_api_key_id — the api-key whose authenticated request created
--                           the instance. NULL for anonymous-mode-created rows.
ALTER TABLE rimsky_instances
    ADD COLUMN service_bindings JSONB,
    ADD COLUMN created_by_api_key_id UUID REFERENCES rimsky_api_keys(id);

-- Extend the scope_kind CHECK constraint. 001-schema.sql declares the CHECK
-- inline on the column, so Postgres auto-named it via the
-- <table>_<column>_check convention. Drop and recreate with the expanded
-- value set ('template','instance') + the new 'run_scope'.
ALTER TABLE rimsky_lifecycle_idempotencies
    DROP CONSTRAINT rimsky_lifecycle_idempotencies_scope_kind_check,
    ADD CONSTRAINT rimsky_lifecycle_idempotencies_scope_kind_check
        CHECK (scope_kind IN ('template', 'instance', 'run_scope'));

-- Extend the state CHECK constraint with the new run_scope_terminal value.
-- The existing vocabulary copied verbatim from 001-schema.sql is
-- ('registered','deployed','undeployed','created'); append 'run_scope_terminal'.
ALTER TABLE rimsky_lifecycle_idempotencies
    DROP CONSTRAINT rimsky_lifecycle_idempotencies_state_check,
    ADD CONSTRAINT rimsky_lifecycle_idempotencies_state_check
        CHECK (state IN ('registered', 'deployed', 'undeployed', 'created', 'run_scope_terminal'));
