-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 006-waitset-topic-kind-taxonomy.sql
--
-- Broaden the rimsky_wait_set.topic_kind CHECK to the full 5-value signal
-- taxonomy (spec 2026-06-06-comprehensive-gap-closure-design, story
-- S-cascade-waitset-topic-taxonomy). This is the CHECK-broadening migration
-- deferred from the 2026-05-23 signal-taxonomy reshape.
--
-- The legacy CHECK admitted only ('state','attribute','event'), forcing the
-- wait-set mapper to fold the new top-level signal kinds (terminal,
-- transient, message) onto the lossy 'state' bucket. With the broadened
-- CHECK each top-level kind reads its own value, so the topic_kind ledger
-- is a faithful projection of the signal class an edge gates on.
--
-- 'state' stays admitted — back-compat for any existing 'state' rows, the
-- empty/unrecognized fallback in waitSetTopicKindFor, and the conformance
-- fixtures. topic_kind is part of the PRIMARY KEY, but its CHECK is
-- independent of PK membership, so dropping/re-adding the CHECK leaves the
-- key untouched. No table rebuild needed on postgres.
--
-- The 001-schema.sql inline CHECK gets a generated name
-- (rimsky_wait_set_topic_kind_check) from postgres; drop it by that name and
-- re-add a named CHECK with the broadened value set.

ALTER TABLE rimsky_wait_set
    DROP CONSTRAINT rimsky_wait_set_topic_kind_check;
ALTER TABLE rimsky_wait_set
    ADD CONSTRAINT rimsky_wait_set_topic_kind_check
    CHECK (topic_kind IN ('state','attribute','event','transient','message','terminal'));
