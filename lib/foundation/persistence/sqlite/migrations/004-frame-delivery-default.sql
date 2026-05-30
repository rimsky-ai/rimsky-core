-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 004-frame-delivery-default.sql — parallel to postgres 004. Per spec
-- 2026-05-29-console-upstream-auth-audit-and-fixes §7c the default
-- FrameDeliveryMode for new instances flips from 'coalesce' to
-- 'serial_queue'.
--
-- Intentionally a NO-OP on SQLite. SQLite has no `ALTER TABLE ... ALTER
-- COLUMN ... SET DEFAULT`; the only way to change an existing column's
-- DEFAULT is the create-new / copy / drop / rename table-rebuild idiom.
-- rimsky_instances cannot be rebuilt safely here: eleven child tables
-- reference it with ON DELETE CASCADE (plus the mutual DEFERRABLE FK to
-- rimsky_run_scopes), and the migration runs inside a single transaction
-- where `PRAGMA foreign_keys=OFF` is a no-op — so dropping the parent
-- would cascade-delete every child row. A destructive rebuild to flip a
-- belt-and-suspenders column DEFAULT is not worth that risk.
--
-- The load-bearing default lives in the driver INSERT literal
-- (COALESCE(?, 'serial_queue') in sqlite/instances.go), which decides the
-- value when the caller omits a mode — so new instances default to
-- serial_queue regardless of the column DEFAULT. The 001-schema.sql column
-- DEFAULT 'coalesce' is dead for the normal create path; the CHECK
-- ('serial_queue','coalesce') still permits both values. This file exists
-- to keep the numbered/append-only migration sequence parallel with the
-- postgres driver.

SELECT 1;
