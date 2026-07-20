-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: run-scope
-- @concept: frame
--
-- 029-frame-root-run-scope-fk.sql
--
-- rimsky_frames.root_run_scope_id (added by 015) carried no FK, so the
-- column could point at a nonexistent rimsky_run_scopes row. Every
-- rimsky_frames insert since 015 (frame.openRunningFrameForMessage)
-- creates the root run scope row before the frame row in the same
-- transaction, so a plain (non-deferred) FK is safe. The leak this
-- enabled — one stranded root run-scope row per frame reaped by
-- PruneTraceForRetention — is closed in code: the prune now captures
-- each pruned frame's root_run_scope_id and deletes those rows after
-- the frame delete, once every non-root child scope has already
-- cascaded away via rimsky_node_runs.
--
-- Pre-v1: a surviving pre-015 zero-UUID placeholder row fails this
-- migration; nuke the dev database rather than special-casing it.

ALTER TABLE rimsky_frames
    ADD CONSTRAINT rimsky_frames_root_run_scope_id_fkey
    FOREIGN KEY (root_run_scope_id) REFERENCES rimsky_run_scopes(id);
