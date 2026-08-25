-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: instance
-- @concept: host-daemon-proxy
-- @concept: anonymous-mode

-- 038-instance-target-routing-identity.sql
--
-- Add target_routing_identity to rimsky_instances: the host-daemon-proxy's
-- routing key for late-bound work — the api-key id for owner-created
-- instances, the anonymous agent's silly-name for anonymous-mode instances.
-- Pre-v1: any existing rows are back-stamped with the owner's api-key id
-- (or an unroutable placeholder for anonymous-created rows).
--
-- The column carries no DEFAULT and a CHECK (length > 0) so every INSERT
-- must provide an explicit non-empty value; SQLite CHECK constraints can
-- only be added via table rebuild, so we rebuild rimsky_instances here.
-- The migrator runs each migration with foreign_keys OFF (see migrate.go),
-- so DROP TABLE does not fire ON DELETE CASCADE into child rows; children
-- reference rimsky_instances(id) by name and re-resolve against the
-- rebuilt table without any child-side ALTER, and the migrator runs
-- PRAGMA foreign_key_check before commit.

CREATE TABLE rimsky_instances_new (
    id                                TEXT PRIMARY KEY,
    template_hash                     TEXT NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key                      TEXT,
    params                            TEXT NOT NULL DEFAULT '{}',
    attribute_overrides               TEXT NOT NULL DEFAULT '{}',
    created_at                        TEXT NOT NULL DEFAULT (datetime('now')),
    terminated_at                     TEXT,
    paused                            INTEGER NOT NULL DEFAULT 0,
    service_bindings                  TEXT,
    created_by_api_key_id             TEXT REFERENCES rimsky_api_keys(id),
    message_queue_mode                TEXT NOT NULL DEFAULT 'backlog'
        CHECK (message_queue_mode IN ('backlog','coalesce')),
    target_routing_identity           TEXT NOT NULL
        CHECK (length(target_routing_identity) > 0),
    UNIQUE (template_hash, instance_key)
);

INSERT INTO rimsky_instances_new (
    id, template_hash, instance_key, params, attribute_overrides,
    created_at, terminated_at, paused, service_bindings, created_by_api_key_id,
    message_queue_mode, target_routing_identity
)
SELECT
    i.id, i.template_hash, i.instance_key, i.params, i.attribute_overrides,
    i.created_at, i.terminated_at, i.paused, i.service_bindings, i.created_by_api_key_id,
    i.message_queue_mode,
    CASE
        WHEN i.created_by_api_key_id IS NOT NULL AND i.created_by_api_key_id <> ''
            THEN i.created_by_api_key_id
        ELSE 'legacy-anonymous-unroutable-' || i.id
    END
FROM rimsky_instances AS i;

DROP TABLE rimsky_instances;

ALTER TABLE rimsky_instances_new RENAME TO rimsky_instances;

CREATE INDEX idx_rimsky_instances_terminated
    ON rimsky_instances (terminated_at)
    WHERE terminated_at IS NOT NULL;

CREATE INDEX idx_rimsky_instances_target_routing_identity
    ON rimsky_instances (target_routing_identity);
