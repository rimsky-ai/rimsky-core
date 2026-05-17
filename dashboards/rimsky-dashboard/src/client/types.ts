// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// Types matching the Rimsky observability API responses (spec §1.2).
//
// These are the public contract the dashboard renders. Field names
// match the JSON envelope on the wire — snake_case throughout per
// spec §1.2 / §1.3.

export type ReachabilityStatus = 'reachable' | 'unreachable' | 'degraded';

// CustomUI is the operator-declared peer-side embedded UI surface.
// `dispatch_url_template` is the per-peer template string (e.g.
// "/trace/{dispatch_id}" for executors or "/claims/{claim_id}" for
// stores) that the dashboard substitutes against — the proto reuses
// one field name across both peer kinds, so there is no separate
// claim_url_template on the wire.
export type CustomUI = {
  ui_url: string;
  embed_mode: 'LINK' | 'IFRAME' | 'BOTH' | 'EMBED_MODE_UNSPECIFIED';
  dispatch_url_template?: string;
};

export type AdminViewParam = {
  name: string;
  type: string;
  description?: string;
  required: boolean;
};

export type AdminViewDecl = {
  name: string;
  title: string;
  description?: string;
  params?: AdminViewParam[];
};

export type ObservabilityCapabilities = {
  supports_trace_get?: boolean;
  supports_trace_stream?: boolean;
  supports_claim_get?: boolean;
  supports_claim_stream?: boolean;
  supports_list_claims?: boolean;
  retention_after_terminal_seconds: number;
  custom_ui?: CustomUI | null;
  admin_views?: AdminViewDecl[];
  http_bridge_url?: string;
};

export type PeerEntry = {
  name: string;
  endpoint: string;
  observability_endpoint: string;
  http_bridge_url?: string;
  reachability_status: ReachabilityStatus;
  observability_capabilities?: ObservabilityCapabilities | null;
  last_probed_at?: string;
  last_error?: string;
};

export type PeerListResponse = {
  stores?: PeerEntry[];
  executors?: PeerEntry[];
};

export type SystemSummary = {
  node_counts: Record<string, number>;
  instances_active: number;
  instances_terminated: number;
  node_runs_claimed?: number;
  node_runs_pending?: number;
};

export type SupervisorRow = {
  id: string;
  accepted_executors: string[];
  accepted_stores: string[];
  concurrency: number;
  callback_host: string;
  callback_port: number;
  last_heartbeat_at: string;
  active_node_count: number;
  registered_at: string;
};

export type SystemHealth = {
  control_api_status: string;
  postgres_status?: string;
  supervisors: SupervisorRow[];
  executors: PeerEntry[];
  stores: PeerEntry[];
};

export type TemplateRow = {
  id: string;
  state: string;
  registered_at: string;
  source: string;
  spec?: any;
};

export type TemplateListResponse = {
  templates: TemplateRow[];
  next_cursor: string;
};

export type TemplateDetail = {
  template: TemplateRow;
  tags: { tag: string; template_id: string; updated_at: string }[];
};

export type InstanceRow = {
  id: string;
  template_hash: string;
  instance_key?: string | null;
  params?: Record<string, any>;
  created_at: string;
  terminated_at?: string | null;
};

export type InstanceListResponse = {
  instances: InstanceRow[];
  next_cursor: string;
};

export type CascadeNode = {
  node_type: string;
  node_id: string;
  state: string;
  current_error_class?: string | null;
  retry_counter: number;
  active_dispatch_id?: string | null;
  last_terminal_event?: { kind: string; occurred_at: string } | null;
  edges_in: string[];
  edges_out: string[];
};

export type InstanceDetail = {
  instance: InstanceRow;
  cascade_graph: CascadeNode[];
};

export type FrameRow = {
  frame_id: string;
  instance_id: string;
  state: string;
  mode: string;
  started_at?: string | null;
  ended_at?: string | null;
  frame_timeout_ms: number;
};

export type FrameListResponse = {
  frames: FrameRow[];
  next_cursor: string;
};

export type NodeRunRow = {
  id: string;
  node_id: string;
  executor_name?: string | null;
  state: 'pending' | 'claimed';
  claimed_by?: string | null;
  enqueued_at: string;
  claimed_at?: string | null;
  last_heartbeat_at?: string | null;
  frame_id: string;
};

export type NodeRunListResponse = {
  node_runs: NodeRunRow[];
  next_cursor: string;
};

export type NodeRunDetail = {
  id: string;
  claim_id?: string | null;
  node_id: string;
  // instance_id and node_type are surfaced so the dashboard can resolve
  // the executor's CustomUI dispatch_url_template substitution markers
  // per spec §2.2 — those markers ({dispatch_id}, {instance_id},
  // {node_type}) are only meaningful on a per-dispatch page.
  instance_id?: string | null;
  node_type?: string | null;
  executor_name?: string | null;
  state: 'pending' | 'claimed';
  claimed_by?: string | null;
  claimed_at?: string | null;
  last_heartbeat_at?: string | null;
  enqueued_at?: string | null;
  frame_id?: string | null;
};

export type LockHolderRow = {
  claim_id: string;
  lock_kind: string;
  lock_name?: string | null;
  producer_name?: string | null;
  region_data?: string;
  intent?: string | null;
  holder_supervisor_id: string;
  holder_node_id: string;
  claimed_at: string;
  last_heartbeat_at: string;
  expires_at: string;
  frame_id?: string | null;
};

export type LockHolderListResponse = {
  lock_holders: LockHolderRow[];
  next_cursor?: string;
};

export type ClaimHolderRow = {
  id: string;
  lock_holder_id: string;
  holder_node_id: string;
  state: string;
  completed_at?: string | null;
};

export type LockHolderDetail = {
  lock_holder: LockHolderRow;
  claim_holders: ClaimHolderRow[];
};

export type EventRow = {
  id: number;
  instance_id?: string | null;
  node_id?: string | null;
  kind: string;
  payload: Record<string, any>;
  occurred_at: string;
};

export type EventListResponse = {
  events: EventRow[];
  next_cursor: string;
};

export type NodeRow = {
  id: string;
  instance_id: string;
  node_type: string;
  executor: string;
  state: string;
  current_error_class?: string;
  retry_counter: number;
  last_heartbeat_at?: string | null;
  assigned_supervisor_id?: string;
  frame_id?: string | null;
  created_at?: string;
  updated_at?: string;
};

export type NodeDetail = {
  node: NodeRow;
  events: EventRow[];
  holdings: LockHolderRow[];
};

// --- Trace (executor observability) ---

export type Severity = 'DEBUG' | 'INFO' | 'WARN' | 'ERROR';

export type TraceEvent = {
  event_id: string;
  parent_event_id?: string;
  timestamp: string;
  severity: Severity;
  category: string;
  message?: string;
  attributes?: Record<string, any>;
};

export type TraceResponse = {
  dispatch_id: string;
  evicted: boolean;
  complete: boolean;
  events: TraceEvent[];
};

// --- Claim (store observability) ---

export type ClaimDetail = {
  claim_id: string;
  state: 'OPEN' | 'COMMITTED' | 'ABANDONED' | 'RELEASED' | 'UNKNOWN' | 'CLAIM_STATE_UNSPECIFIED';
  address?: any;
  payload?: any;
  region?: any;
  opened_at?: string;
  closed_at?: string;
  history?: TraceEvent[];
};

export type ClaimSummary = {
  claim_id: string;
  state: string;
  opened_at?: string;
  closed_at?: string;
};

export type ClaimList = {
  claims: ClaimSummary[];
  next_cursor: string;
};

// --- Admin views ---

export type AdminViewColumn = { name: string; type: string };
export type AdminViewSchema = { columns: AdminViewColumn[] };
export type AdminViewResponse = {
  schema: AdminViewSchema;
  data: { rows: any[] };
  render_hint: string;
};

// --- Parked-node diagnostics ---

export type ParkedNodeEntry = {
  instance_id: string;
  node_id: string;
  parked_at: string;
  resume_at?: string;
  reason?: string;
  reason_note?: string;
};

export type ParkedNodesResponse = {
  parked_nodes: ParkedNodeEntry[];
};

// --- Asset surface (2026-05-15 data-platform-extensions) ---
//
// Assets are the documented compound: claim against a `DataProcessing`-
// capable producer + `lifetime: durable`. The dashboard's asset-primary
// panel reads these via the control-api `/instances/{id}/assets/...`
// endpoint family.

export type AssetRow = {
  instance_id: string;
  alias: string;
  producer_name: string;
  scope_data_hash: string;
  current_version_id?: string | null;
  held_durable: boolean;
  created_at: string;
};

export type AssetListResponse = {
  assets: AssetRow[];
  next_cursor?: string;
};

export type AssetVersionRow = {
  version_id: string;
  committed_at: string;
  metadata?: unknown;
};

export type AssetVersionsResponse = {
  versions: AssetVersionRow[];
};

export type AssetMaterializationRow = {
  version_id: string;
  parent_run_id?: string | null;
  frame_id?: string | null;
  committed_at: string;
};

export type AssetMaterializationHistoryResponse = {
  materializations: AssetMaterializationRow[];
};

export type AssetLineageEdge = {
  run_id?: string;
  claim_handle_id?: string;
  kind: string;
};

export type AssetDetail = {
  asset: AssetRow;
  versions: AssetVersionRow[];
  materializations: AssetMaterializationRow[];
  upstream?: AssetLineageEdge[];
  downstream?: AssetLineageEdge[];
};
