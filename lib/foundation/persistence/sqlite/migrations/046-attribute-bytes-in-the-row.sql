-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @decision: attribute-bytes-in-the-row
--
-- 046-attribute-bytes-in-the-row.sql
--
-- See the postgres sibling migration. The attribute table changes two
-- column types, so SQLite requires a table rebuild (the migration-021
-- pattern): the migrator applies every migration with foreign_keys OFF,
-- so DROP TABLE performs no implicit cascade, and the table's one index
-- is recreated below. rimsky_node_runs takes a column rename and two
-- column drops, neither of which touches an index.

CREATE TABLE rimsky_node_attributes_new (
    node_run_id                  TEXT PRIMARY KEY REFERENCES rimsky_node_runs(id) ON DELETE CASCADE,
    node_id                      TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    data                         BLOB NOT NULL DEFAULT '{}',
    dispatch_input_bag           BLOB,
    updated_at                   TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO rimsky_node_attributes_new
    (node_run_id, node_id, data, dispatch_input_bag, updated_at)
SELECT node_run_id, node_id, CAST(data AS BLOB), CAST(dispatch_input_bag AS BLOB), updated_at
  FROM rimsky_node_attributes;

DROP TABLE rimsky_node_attributes;

ALTER TABLE rimsky_node_attributes_new RENAME TO rimsky_node_attributes;

CREATE INDEX idx_rimsky_node_attributes_node
    ON rimsky_node_attributes (node_id, updated_at DESC);

ALTER TABLE rimsky_node_runs RENAME COLUMN scratch_inline TO scratch;

ALTER TABLE rimsky_node_runs DROP COLUMN scratch_handle;

ALTER TABLE rimsky_node_runs DROP COLUMN scratch_handle_backend;

DROP INDEX IF EXISTS idx_rimsky_blob_orphans_reap;

DROP TABLE IF EXISTS rimsky_blob_orphans;
