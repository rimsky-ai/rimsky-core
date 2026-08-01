-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: module-layout
--
-- 035-index-naming-uniformity.sql
--
-- Index names mixed four conventions (idx_rimsky_*, idx_* without the
-- rimsky_ prefix, rimsky_*_idx, uq_*): tables are uniformly rimsky_-
-- prefixed but a third of the indexes weren't, and an embedding consumer
-- sharing the schema could collide with an unprefixed name. Standardize
-- on idx_rimsky_<descriptor> / uq_rimsky_<descriptor> everywhere; this
-- renames the live index (whichever migration originally created it),
-- not the historical CREATE INDEX statements.

ALTER INDEX idx_messages_pending RENAME TO idx_rimsky_messages_pending;
ALTER INDEX idx_messages_instance_received RENAME TO idx_rimsky_messages_instance_received;
ALTER INDEX idx_messages_frame_id RENAME TO idx_rimsky_messages_frame_id;
ALTER INDEX idx_node_runs_dispatch_order RENAME TO idx_rimsky_node_runs_dispatch_order;
ALTER INDEX idx_node_runs_node_frame RENAME TO idx_rimsky_node_runs_node_frame;
ALTER INDEX idx_node_runs_run_scope RENAME TO idx_rimsky_node_runs_run_scope;
ALTER INDEX idx_node_run_parked_resume RENAME TO idx_rimsky_node_run_parked_resume;
ALTER INDEX idx_claim_handles_parent RENAME TO idx_rimsky_claim_handles_parent;
ALTER INDEX idx_lineage_run RENAME TO idx_rimsky_lineage_run;
ALTER INDEX idx_lineage_claim RENAME TO idx_rimsky_lineage_claim;
ALTER INDEX idx_lineage_substitution_refs RENAME TO idx_rimsky_lineage_substitution_refs;
ALTER INDEX idx_blob_orphans_reap RENAME TO idx_rimsky_blob_orphans_reap;
ALTER INDEX idx_bp_hits_breakpoint_seq RENAME TO idx_rimsky_bp_hits_breakpoint_seq;
ALTER INDEX idx_bp_hits_breakpoint_unresumed RENAME TO idx_rimsky_bp_hits_breakpoint_unresumed;
ALTER INDEX idx_bp_hits_instance_seq RENAME TO idx_rimsky_bp_hits_instance_seq;
ALTER INDEX idx_breakpoints_expires RENAME TO idx_rimsky_breakpoints_expires;
ALTER INDEX idx_breakpoints_instance_active RENAME TO idx_rimsky_breakpoints_instance_active;
ALTER INDEX idx_publisher_subscriptions_instance RENAME TO idx_rimsky_publisher_subscriptions_instance;
ALTER INDEX idx_publisher_subscriptions_state RENAME TO idx_rimsky_publisher_subscriptions_state;
ALTER INDEX idx_message_idempotencies_created_at RENAME TO idx_rimsky_message_idempotencies_created_at;
ALTER INDEX idx_run_scopes_parent_chain RENAME TO idx_rimsky_run_scopes_parent_chain;
ALTER INDEX rimsky_api_keys_active_name_idx RENAME TO idx_rimsky_api_keys_active_name;
ALTER INDEX rimsky_api_keys_active_status_idx RENAME TO idx_rimsky_api_keys_active_status;
ALTER INDEX rimsky_api_keys_revoke_at_pending_idx RENAME TO idx_rimsky_api_keys_revoke_at_pending;
ALTER INDEX rimsky_claim_handles_active_idx RENAME TO idx_rimsky_claim_handles_active;
ALTER INDEX rimsky_claim_handles_committed_durable_idx RENAME TO idx_rimsky_claim_handles_committed_durable;
ALTER INDEX rimsky_events_audit_action_idx RENAME TO idx_rimsky_events_audit_action;
ALTER INDEX rimsky_events_audit_key_id_idx RENAME TO idx_rimsky_events_audit_key_id;
ALTER INDEX rimsky_events_audit_key_name_idx RENAME TO idx_rimsky_events_audit_key_name;
ALTER INDEX rimsky_events_audit_mode_idx RENAME TO idx_rimsky_events_audit_mode;
ALTER INDEX rimsky_events_audit_request_path_idx RENAME TO idx_rimsky_events_audit_request_path;
ALTER INDEX rimsky_events_audit_status_idx RENAME TO idx_rimsky_events_audit_status;
ALTER INDEX rimsky_events_instance_id_occurred_at_idx RENAME TO idx_rimsky_events_instance_id_occurred_at;
ALTER INDEX rimsky_events_kind_occurred_at_idx RENAME TO idx_rimsky_events_kind_occurred_at;
ALTER INDEX rimsky_events_node_id_occurred_at_idx RENAME TO idx_rimsky_events_node_id_occurred_at;
ALTER INDEX rimsky_node_attributes_node_idx RENAME TO idx_rimsky_node_attributes_node;
ALTER INDEX rimsky_node_runs_async_ack_id_idx RENAME TO idx_rimsky_node_runs_async_ack_id;
ALTER INDEX rimsky_node_runs_claimed_idx RENAME TO idx_rimsky_node_runs_claimed;
ALTER INDEX rimsky_node_runs_stale_idx RENAME TO idx_rimsky_node_runs_stale;
ALTER INDEX rimsky_node_runs_state_idx RENAME TO idx_rimsky_node_runs_state;
ALTER INDEX rimsky_nodes_instance_id_node_type_idx RENAME TO idx_rimsky_nodes_instance_id_node_type;
ALTER INDEX rimsky_nodes_tags_idx RENAME TO idx_rimsky_nodes_tags;
ALTER INDEX rimsky_producer_verb_outbox_producer_seq RENAME TO idx_rimsky_producer_verb_outbox_producer_seq;
ALTER INDEX uq_node_runs_serialization_gate RENAME TO uq_rimsky_node_runs_serialization_gate;
ALTER INDEX uq_run_scopes_fanout_partition_open RENAME TO uq_rimsky_run_scopes_fanout_partition_open;
