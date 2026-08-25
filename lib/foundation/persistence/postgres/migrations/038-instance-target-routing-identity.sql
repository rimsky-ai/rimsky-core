-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: instance
-- @concept: host-daemon-proxy
-- @concept: anonymous-mode

-- 038-instance-target-routing-identity.sql
--
-- Add target_routing_identity to rimsky_instances: the host-daemon-proxy's
-- routing key for late-bound work — the api-key id for owner-created
-- instances, the anonymous agent's silly-name for anonymous-mode instances.
-- Pre-v1: no existing anonymous instances survive this change (rimsky
-- carries no anonymous-instance routing continuity guarantee across this
-- migration), so any pre-existing rows are stamped with their audit key id
-- verbatim and anonymous rows adopt an unroutable placeholder that will
-- surface as agent-not-connected until they are re-created.

ALTER TABLE rimsky_instances
    ADD COLUMN target_routing_identity TEXT NOT NULL DEFAULT '';

UPDATE rimsky_instances
    SET target_routing_identity = created_by_api_key_id::text
    WHERE created_by_api_key_id IS NOT NULL;

UPDATE rimsky_instances
    SET target_routing_identity = 'legacy-anonymous-unroutable-' || id::text
    WHERE target_routing_identity = '';

ALTER TABLE rimsky_instances
    ALTER COLUMN target_routing_identity DROP DEFAULT;

ALTER TABLE rimsky_instances
    ADD CONSTRAINT rimsky_instances_target_routing_identity_nonempty
    CHECK (length(target_routing_identity) > 0);

CREATE INDEX idx_rimsky_instances_target_routing_identity
    ON rimsky_instances (target_routing_identity);
