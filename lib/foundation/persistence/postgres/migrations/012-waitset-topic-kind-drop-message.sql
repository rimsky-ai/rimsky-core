-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 011-waitset-topic-kind-drop-message.sql
--
-- Drop 'message' from the rimsky_wait_set.topic_kind CHECK admitted set.
-- Pass 4 of the 2026-06-14 message-schema-layer reshape: the `message/*`
-- top-level kind retires from the canonical signal taxonomy. Message
-- arrival becomes a virtual-node settle whose subscribers wake via
-- stale-marking, NOT via wait-set rows keyed on a virtual sender run —
-- so no wait-set row can carry a 'message' topic_kind under the new
-- model.
--
-- Tightening the CHECK at the schema level keeps a regression from
-- silently re-introducing 'message' rows by surfacing the rejection at
-- the persistence boundary. The admitted set remains aligned with the
-- four canonical kinds plus the 'state' defensive fallback.
--
-- The 006-waitset-topic-kind-taxonomy migration named the CHECK
-- constraint `rimsky_wait_set_topic_kind_check`; drop and re-add under
-- the same name with the tightened value set.
--
-- No BEGIN/COMMIT wrapper: the migrator's ApplyOne already opens a tx
-- around the script execution, so wrapping here would nest transactions.

-- Drop any stale wait_set rows carrying the retired 'message' topic_kind
-- BEFORE tightening the CHECK. Mirrors the SQLite migration's filter
-- (`WHERE topic_kind <> 'message'` on the rebuild SELECT): both backends
-- now have the same post-migration row count given the same input, so
-- the cross-backend conformance scenarios stay in lockstep. Without
-- this delete, Postgres's `ADD CONSTRAINT ... CHECK` would validate
-- existing rows and fail loudly on any populated dev DB while SQLite
-- silently dropped them — the inconsistency would surface as
-- divergent diagnostics on cross-backend test runs. Pre-v1 has no
-- backwards-compat duty per .claude/rules/rules.md; the 'message'
-- topic_kind retires entirely with Pass 4 of the 2026-06-14 message-
-- schema-layer reshape.
DELETE FROM rimsky_wait_set WHERE topic_kind = 'message';

ALTER TABLE rimsky_wait_set
    DROP CONSTRAINT rimsky_wait_set_topic_kind_check;
ALTER TABLE rimsky_wait_set
    ADD CONSTRAINT rimsky_wait_set_topic_kind_check
    CHECK (topic_kind IN ('state','attribute','event','transient','terminal'));
