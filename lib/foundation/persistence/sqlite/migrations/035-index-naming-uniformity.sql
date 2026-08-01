-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: module-layout
--
-- 035-index-naming-uniformity.sql
--
-- See the postgres sibling migration for rationale. SQLite has no ALTER
-- INDEX RENAME, so each entry is a DROP + CREATE reproducing the index's
-- current definition (as last redefined by 001/017/026/027/028) under the
-- new name; two postgres-only GIN indexes (idx_lineage_substitution_refs,
-- rimsky_nodes_tags_idx) have no SQLite counterpart to rename.

DROP INDEX IF EXISTS idx_messages_pending;
CREATE INDEX idx_rimsky_messages_pending
    ON rimsky_messages(instance_id, received_at)
    WHERE delivered_at IS NULL AND cancelled = 0;

DROP INDEX IF EXISTS idx_messages_instance_received;
CREATE INDEX idx_rimsky_messages_instance_received
    ON rimsky_messages(instance_id, received_at);

DROP INDEX IF EXISTS idx_messages_frame_id;
CREATE INDEX idx_rimsky_messages_frame_id
    ON rimsky_messages(frame_id)
    WHERE frame_id IS NOT NULL;

DROP INDEX IF EXISTS idx_node_runs_dispatch_order;
CREATE INDEX idx_rimsky_node_runs_dispatch_order
    ON rimsky_node_runs (node_id, run_scope_id, frame_id, sequence);

DROP INDEX IF EXISTS idx_node_runs_node_frame;
CREATE INDEX idx_rimsky_node_runs_node_frame
    ON rimsky_node_runs(node_id, frame_id);

DROP INDEX IF EXISTS idx_node_runs_run_scope;
CREATE INDEX idx_rimsky_node_runs_run_scope ON rimsky_node_runs (run_scope_id);

DROP INDEX IF EXISTS idx_node_run_parked_resume;
CREATE INDEX idx_rimsky_node_run_parked_resume
    ON rimsky_node_runs(resume_at)
    WHERE state = 'parked' AND resume_at IS NOT NULL;

DROP INDEX IF EXISTS idx_claim_handles_parent;
CREATE INDEX idx_rimsky_claim_handles_parent
    ON rimsky_claim_handles(parent_claim_handle_id)
    WHERE parent_claim_handle_id IS NOT NULL;

DROP INDEX IF EXISTS idx_lineage_run;
CREATE INDEX idx_rimsky_lineage_run
    ON rimsky_lineage(record_kind, json_extract(record, '$.run_id'));

DROP INDEX IF EXISTS idx_lineage_claim;
CREATE INDEX idx_rimsky_lineage_claim
    ON rimsky_lineage(record_kind, json_extract(record, '$.claim_handle_id'));

DROP INDEX IF EXISTS idx_blob_orphans_reap;
CREATE INDEX idx_rimsky_blob_orphans_reap
    ON rimsky_blob_orphans(reap_after);

DROP INDEX IF EXISTS idx_bp_hits_breakpoint_seq;
CREATE INDEX idx_rimsky_bp_hits_breakpoint_seq
    ON rimsky_breakpoint_hits (breakpoint_id, seq);

DROP INDEX IF EXISTS idx_bp_hits_breakpoint_unresumed;
CREATE INDEX idx_rimsky_bp_hits_breakpoint_unresumed
    ON rimsky_breakpoint_hits (breakpoint_id, hit_at)
    WHERE resumed_at IS NULL;

DROP INDEX IF EXISTS idx_bp_hits_instance_seq;
CREATE INDEX idx_rimsky_bp_hits_instance_seq
    ON rimsky_breakpoint_hits (instance_id, seq);

DROP INDEX IF EXISTS idx_breakpoints_expires;
CREATE INDEX idx_rimsky_breakpoints_expires
    ON rimsky_instance_breakpoints (expires_at)
    WHERE expires_at IS NOT NULL;

DROP INDEX IF EXISTS idx_breakpoints_instance_active;
CREATE INDEX idx_rimsky_breakpoints_instance_active
    ON rimsky_instance_breakpoints (instance_id)
    WHERE expires_at IS NULL;

DROP INDEX IF EXISTS idx_publisher_subscriptions_instance;
CREATE INDEX idx_rimsky_publisher_subscriptions_instance
    ON rimsky_publisher_subscriptions(instance_id);

DROP INDEX IF EXISTS idx_publisher_subscriptions_state;
CREATE INDEX idx_rimsky_publisher_subscriptions_state
    ON rimsky_publisher_subscriptions(state)
    WHERE state IN ('mounting','active');

DROP INDEX IF EXISTS idx_message_idempotencies_created_at;
CREATE INDEX idx_rimsky_message_idempotencies_created_at
    ON rimsky_message_idempotencies(created_at);

DROP INDEX IF EXISTS idx_run_scopes_parent_chain;
CREATE INDEX idx_rimsky_run_scopes_parent_chain ON rimsky_run_scopes (parent_run_scope_id);

DROP INDEX IF EXISTS rimsky_api_keys_active_name_idx;
CREATE UNIQUE INDEX idx_rimsky_api_keys_active_name
    ON rimsky_api_keys (name)
    WHERE revoked_at IS NULL AND revoke_at IS NULL;

DROP INDEX IF EXISTS rimsky_api_keys_active_status_idx;
CREATE INDEX idx_rimsky_api_keys_active_status
    ON rimsky_api_keys (revoked_at, expires_at, revoke_at);

DROP INDEX IF EXISTS rimsky_api_keys_revoke_at_pending_idx;
CREATE INDEX idx_rimsky_api_keys_revoke_at_pending
    ON rimsky_api_keys (revoke_at)
    WHERE revoke_at IS NOT NULL AND revoked_at IS NULL;

DROP INDEX IF EXISTS rimsky_claim_handles_active_idx;
CREATE INDEX idx_rimsky_claim_handles_active
    ON rimsky_claim_handles (holder_supervisor_id) WHERE state = 'active';

DROP INDEX IF EXISTS rimsky_claim_handles_committed_durable_idx;
CREATE INDEX idx_rimsky_claim_handles_committed_durable
    ON rimsky_claim_handles (holder_node_id) WHERE state = 'committed' AND lifetime = 'durable';

DROP INDEX IF EXISTS rimsky_events_audit_action_idx;
CREATE INDEX idx_rimsky_events_audit_action
    ON rimsky_events (json_extract(payload, '$.action'))
    WHERE kind LIKE 'auth.%';

DROP INDEX IF EXISTS rimsky_events_audit_key_id_idx;
CREATE INDEX idx_rimsky_events_audit_key_id
    ON rimsky_events (json_extract(payload, '$.key_id'))
    WHERE kind LIKE 'auth.%';

DROP INDEX IF EXISTS rimsky_events_audit_key_name_idx;
CREATE INDEX idx_rimsky_events_audit_key_name
    ON rimsky_events (json_extract(payload, '$.key_name'))
    WHERE kind LIKE 'auth.%';

DROP INDEX IF EXISTS rimsky_events_audit_mode_idx;
CREATE INDEX idx_rimsky_events_audit_mode
    ON rimsky_events (json_extract(payload, '$.mode'))
    WHERE kind LIKE 'auth.%';

DROP INDEX IF EXISTS rimsky_events_audit_request_path_idx;
CREATE INDEX idx_rimsky_events_audit_request_path
    ON rimsky_events (json_extract(payload, '$.request_path'))
    WHERE kind LIKE 'auth.%';

DROP INDEX IF EXISTS rimsky_events_audit_status_idx;
CREATE INDEX idx_rimsky_events_audit_status
    ON rimsky_events (json_extract(payload, '$.response_status'))
    WHERE kind LIKE 'auth.%';

DROP INDEX IF EXISTS rimsky_events_instance_id_occurred_at_idx;
CREATE INDEX idx_rimsky_events_instance_id_occurred_at ON rimsky_events (instance_id, occurred_at DESC);

DROP INDEX IF EXISTS rimsky_events_kind_occurred_at_idx;
CREATE INDEX idx_rimsky_events_kind_occurred_at ON rimsky_events (kind, occurred_at DESC);

DROP INDEX IF EXISTS rimsky_events_node_id_occurred_at_idx;
CREATE INDEX idx_rimsky_events_node_id_occurred_at ON rimsky_events (node_id, occurred_at DESC);

DROP INDEX IF EXISTS rimsky_node_attributes_node_idx;
CREATE INDEX idx_rimsky_node_attributes_node
    ON rimsky_node_attributes (node_id, updated_at DESC);

DROP INDEX IF EXISTS rimsky_node_runs_async_ack_id_idx;
CREATE UNIQUE INDEX idx_rimsky_node_runs_async_ack_id
    ON rimsky_node_runs (async_ack_id)
    WHERE async_ack_id IS NOT NULL;

DROP INDEX IF EXISTS rimsky_node_runs_claimed_idx;
CREATE INDEX idx_rimsky_node_runs_claimed
    ON rimsky_node_runs (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;

DROP INDEX IF EXISTS rimsky_node_runs_stale_idx;
CREATE INDEX idx_rimsky_node_runs_stale
    ON rimsky_node_runs (enqueued_at) WHERE state = 'stale';

DROP INDEX IF EXISTS rimsky_node_runs_state_idx;
CREATE INDEX idx_rimsky_node_runs_state
    ON rimsky_node_runs (state);

DROP INDEX IF EXISTS rimsky_nodes_instance_id_node_type_idx;
CREATE INDEX idx_rimsky_nodes_instance_id_node_type ON rimsky_nodes (instance_id, node_type);

DROP INDEX IF EXISTS rimsky_producer_verb_outbox_producer_seq;
CREATE INDEX idx_rimsky_producer_verb_outbox_producer_seq
    ON rimsky_producer_verb_outbox (producer_name, seq);

DROP INDEX IF EXISTS uq_node_runs_serialization_gate;
CREATE UNIQUE INDEX uq_rimsky_node_runs_serialization_gate
    ON rimsky_node_runs (node_id, run_scope_id)
    WHERE claimed_by IS NOT NULL
       OR state IN ('held','parked');

DROP INDEX IF EXISTS uq_run_scopes_fanout_partition_open;
CREATE UNIQUE INDEX uq_rimsky_run_scopes_fanout_partition_open
    ON rimsky_run_scopes (parent_run_id, partition_key)
    WHERE partition_key != '' AND closed_at IS NULL;
