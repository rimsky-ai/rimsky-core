CREATE TABLE IF NOT EXISTS work_items (
  item_id     TEXT PRIMARY KEY,
  payload     JSONB NOT NULL,
  state       TEXT NOT NULL DEFAULT 'available',
  claim_token TEXT,
  claimed_at  TIMESTAMPTZ,
  enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  priority    INTEGER NOT NULL DEFAULT 0,
  sequence    BIGSERIAL
);
INSERT INTO work_items (item_id, payload, priority) VALUES
  ('job-alpha', '{"topic":"alpha"}', 10),
  ('job-beta',  '{"topic":"beta"}',  5)
ON CONFLICT DO NOTHING;

CREATE SCHEMA IF NOT EXISTS analytics_production;
CREATE TABLE IF NOT EXISTS analytics_production.items (id INT PRIMARY KEY, label TEXT);
INSERT INTO analytics_production.items (id, label) VALUES (1, 'stale') ON CONFLICT DO NOTHING;
