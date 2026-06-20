-- @concept: publisher-subscription
-- Drop the dead `target_node` routing field. Delivery routes by
-- `messages.type` against node-subscription edges; the column has
-- no consumer.
ALTER TABLE rimsky_publisher_subscriptions
    DROP COLUMN target_node;
