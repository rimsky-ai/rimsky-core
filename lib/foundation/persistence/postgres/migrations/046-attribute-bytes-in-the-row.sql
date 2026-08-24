-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @decision: attribute-bytes-in-the-row
--
-- 046-attribute-bytes-in-the-row.sql
--
-- The attribute bag, the dispatch input bag, and node-run scratch become
-- byte columns that commit with their row. The spill handles, the handle
-- backends, and the orphan ledger the external blob store needed go with
-- it. Pre-v1: a deployment that held values behind a spill handle
-- re-creates its instances; nothing carries them over.

ALTER TABLE rimsky_node_attributes
    ALTER COLUMN data DROP DEFAULT;

ALTER TABLE rimsky_node_attributes
    ALTER COLUMN data TYPE BYTEA USING convert_to(data::text, 'UTF8'),
    ALTER COLUMN dispatch_input_bag TYPE BYTEA USING convert_to(dispatch_input_bag::text, 'UTF8');

ALTER TABLE rimsky_node_attributes
    ALTER COLUMN data SET DEFAULT '{}'::bytea;

ALTER TABLE rimsky_node_attributes
    DROP COLUMN value_handle,
    DROP COLUMN value_handle_backend;

ALTER TABLE rimsky_node_runs
    RENAME COLUMN scratch_inline TO scratch;

ALTER TABLE rimsky_node_runs
    DROP COLUMN scratch_handle,
    DROP COLUMN scratch_handle_backend;

DROP TABLE IF EXISTS rimsky_blob_orphans;
