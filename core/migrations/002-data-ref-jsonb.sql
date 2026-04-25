-- 002-data-ref-jsonb.sql
-- Plan C Phase 0.1 — reshape rimsky_resource_versions.data_ref from TEXT to
-- JSONB so external-resource backends can store structured refs (e.g. an
-- {"connection":"warehouse","table":"facts","row_id":123} envelope) without
-- an extra string-encoding layer.
--
-- Any pre-existing TEXT values are preserved under {"legacy": <text>} so no
-- data is lost when this migration lands on a database that already has
-- Plan A external-store rows. Plan A itself only uses inline-jsonb, where
-- data_ref is always NULL, so the CASE is defensive.

ALTER TABLE rimsky_resource_versions
  ALTER COLUMN data_ref TYPE JSONB USING CASE
    WHEN data_ref IS NULL THEN NULL
    ELSE jsonb_build_object('legacy', data_ref)
  END;
