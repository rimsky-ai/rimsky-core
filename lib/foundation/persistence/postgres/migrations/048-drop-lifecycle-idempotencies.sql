-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @decision: lifecycle-subscriber-at-least-once-delivery
--
-- 048-drop-lifecycle-idempotencies.sql
--
-- The outbox row is the whole record of what a service is owed. A
-- delivery the service acknowledged has no row, because the drain
-- deletes it. A delivery an instance still owes has one. The
-- idempotency ledger recorded both facts a second way, so it goes.

DROP INDEX IF EXISTS idx_rimsky_lifecycle_idempotencies_scope;
DROP TABLE IF EXISTS rimsky_lifecycle_idempotencies;
