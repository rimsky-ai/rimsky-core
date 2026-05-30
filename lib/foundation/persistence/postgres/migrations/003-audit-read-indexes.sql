-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 003-audit-read-indexes.sql
--
-- Expression indexes over the auth audit payload keys that back the
-- GET /audit read surface (spec 2026-05-29-console-upstream-auth-audit-
-- and-fixes). The /audit reader filters on JSONB payload keys
-- (key_id / key_name / action / response_status / mode / request_path)
-- restricted to the auth.* event kinds; these partial expression
-- indexes scope to those kinds so the index stays small and only the
-- audit slice is covered. Every filter the handler accepts is
-- index-backed (one expression index per payload-filter dimension).
--
-- occurred_at is already indexed (rimsky_events_kind_occurred_at_idx);
-- these add the payload-key dimension. Mirrors the partial-index style
-- in 001-schema.sql (`... WHERE phase = 'pending'`).

CREATE INDEX rimsky_events_audit_key_id_idx
    ON rimsky_events ((payload->>'key_id'))
    WHERE kind LIKE 'auth.%';

CREATE INDEX rimsky_events_audit_key_name_idx
    ON rimsky_events ((payload->>'key_name'))
    WHERE kind LIKE 'auth.%';

CREATE INDEX rimsky_events_audit_action_idx
    ON rimsky_events ((payload->>'action'))
    WHERE kind LIKE 'auth.%';

CREATE INDEX rimsky_events_audit_status_idx
    ON rimsky_events ((payload->>'response_status'))
    WHERE kind LIKE 'auth.%';

CREATE INDEX rimsky_events_audit_mode_idx
    ON rimsky_events ((payload->>'mode'))
    WHERE kind LIKE 'auth.%';

CREATE INDEX rimsky_events_audit_request_path_idx
    ON rimsky_events ((payload->>'request_path'))
    WHERE kind LIKE 'auth.%';
