-- @concept: publisher-subscription
-- Drop the dead `target_node` routing field. Delivery routes by
-- `messages.type` against node-subscription edges; the column has
-- no consumer.
DROP INDEX IF EXISTS idx_publisher_subscriptions_instance;
DROP INDEX IF EXISTS idx_publisher_subscriptions_state;

CREATE TABLE rimsky_publisher_subscriptions_new (
    id                TEXT NOT NULL,
    instance_id       TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    publisher_name    TEXT NOT NULL,
    kind              TEXT NOT NULL,
    resolved_config   TEXT NOT NULL,
    message_type      TEXT NOT NULL,
    started_at        TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    state             TEXT NOT NULL DEFAULT 'mounting'
        CHECK (state IN ('mounting','active','failed','stopped')),
    failure_reason    TEXT,
    PRIMARY KEY (publisher_name, id)
);

INSERT INTO rimsky_publisher_subscriptions_new
    (id, instance_id, publisher_name, kind, resolved_config,
     message_type, started_at, state, failure_reason)
SELECT id, instance_id, publisher_name, kind, resolved_config,
       message_type, started_at, state, failure_reason
  FROM rimsky_publisher_subscriptions;

DROP TABLE rimsky_publisher_subscriptions;
ALTER TABLE rimsky_publisher_subscriptions_new RENAME TO rimsky_publisher_subscriptions;

CREATE INDEX idx_publisher_subscriptions_instance
    ON rimsky_publisher_subscriptions(instance_id);
CREATE INDEX idx_publisher_subscriptions_state
    ON rimsky_publisher_subscriptions(state)
    WHERE state IN ('mounting','active');
