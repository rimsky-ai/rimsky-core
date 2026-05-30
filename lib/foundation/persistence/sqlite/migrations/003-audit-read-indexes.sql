-- 003-audit-read-indexes.sql
--
-- Expression indexes over the auth audit payload keys that back the
-- GET /audit read surface (spec 2026-05-29-console-upstream-auth-audit-
-- and-fixes). The /audit reader filters on payload keys
-- (key_id / key_name / action / response_status / mode / request_path)
-- via json_extract, restricted to the auth.* event kinds. These partial
-- expression indexes scope to those kinds (matching the driver's List
-- predicates) so they stay small and cover only the audit slice. The
-- index expressions mirror the json_extract() forms used in
-- sqlite/events.go's List so the planner can use them. Every filter the
-- handler accepts is index-backed (one expression index per
-- payload-filter dimension).
--
-- occurred_at is already indexed (rimsky_events_kind_occurred_at_idx);
-- these add the payload-key dimension. Mirrors the partial-index style
-- in 001-schema.sql.

CREATE INDEX rimsky_events_audit_key_id_idx
    ON rimsky_events (json_extract(payload, '$.key_id'))
    WHERE kind LIKE 'auth.%';

CREATE INDEX rimsky_events_audit_key_name_idx
    ON rimsky_events (json_extract(payload, '$.key_name'))
    WHERE kind LIKE 'auth.%';

CREATE INDEX rimsky_events_audit_action_idx
    ON rimsky_events (json_extract(payload, '$.action'))
    WHERE kind LIKE 'auth.%';

CREATE INDEX rimsky_events_audit_status_idx
    ON rimsky_events (json_extract(payload, '$.response_status'))
    WHERE kind LIKE 'auth.%';

CREATE INDEX rimsky_events_audit_mode_idx
    ON rimsky_events (json_extract(payload, '$.mode'))
    WHERE kind LIKE 'auth.%';

CREATE INDEX rimsky_events_audit_request_path_idx
    ON rimsky_events (json_extract(payload, '$.request_path'))
    WHERE kind LIKE 'auth.%';
