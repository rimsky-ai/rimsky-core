// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// Typed fetch wrappers around the dashboard's proxy endpoints.
//
// All paths go through the Hono server's /api/control, /api/exec/:name,
// /api/store/:name proxies (see src/server/proxy.ts). The dashboard
// never talks to control-api / executors / stores directly.

import type {
  ClaimDetail,
  ClaimList,
  NodeRunDetail,
  NodeRunListResponse,
  EventListResponse,
  FrameListResponse,
  InstanceDetail,
  InstanceListResponse,
  LockHolderDetail,
  LockHolderListResponse,
  NodeDetail,
  PeerEntry,
  PeerListResponse,
  ScheduleListResponse,
  SystemHealth,
  SystemSummary,
  TemplateDetail,
  TemplateListResponse,
  TraceResponse,
  AdminViewResponse,
  FrameRow,
  ParkedNodesResponse,
} from './types';

async function get<T>(path: string): Promise<T> {
  const r = await fetch(path);
  if (!r.ok) {
    let detail = '';
    try {
      detail = await r.text();
    } catch (_e) {
      // ignore
    }
    throw new Error(`${r.status} ${r.statusText}: ${detail}`);
  }
  return (await r.json()) as T;
}

function qs(params: Record<string, string | number | undefined | null>) {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue;
    u.set(k, String(v));
  }
  const s = u.toString();
  return s ? `?${s}` : '';
}

export const api = {
  systemHealth: () => get<SystemHealth>('/api/control/system/health'),
  systemSummary: () => get<SystemSummary>('/api/control/system/summary'),
  listStores: () => get<PeerListResponse>('/api/control/stores'),
  getStore: (name: string) => get<{ peer: PeerEntry; lifecycle?: any[] }>(`/api/control/stores/${name}`),
  listExecutors: () => get<PeerListResponse>('/api/control/executors'),
  getExecutor: (name: string) => get<{ peer: PeerEntry }>(`/api/control/executors/${name}`),

  listTemplates: (filters: Record<string, string | undefined> = {}, cursor?: string) =>
    get<TemplateListResponse>(`/api/control/templates${qs({ ...filters, cursor })}`),
  getTemplate: (hash: string) => get<TemplateDetail>(`/api/control/templates/${hash}`),

  listInstances: (filters: Record<string, string | undefined> = {}, cursor?: string) =>
    get<InstanceListResponse>(`/api/control/instances${qs({ ...filters, cursor })}`),
  getInstance: (id: string) => get<InstanceDetail>(`/api/control/instances/${id}`),

  listSchedules: (cursor?: string) =>
    get<ScheduleListResponse>(`/api/control/schedules${qs({ cursor })}`),

  listFrames: (filters: Record<string, string | undefined> = {}, cursor?: string) =>
    get<FrameListResponse>(`/api/control/frames${qs({ ...filters, cursor })}`),
  getFrame: (id: string) => get<{ frame: FrameRow }>(`/api/control/frames/${id}`),

  getNode: (instanceId: string, nodeType: string) =>
    get<NodeDetail>(`/api/control/nodes/${instanceId}/${nodeType}`),

  listNodeRuns: (filters: Record<string, string | undefined> = {}, cursor?: string) =>
    get<NodeRunListResponse>(`/api/control/node-runs${qs({ ...filters, cursor })}`),
  getNodeRun: (id: string) => get<NodeRunDetail>(`/api/control/node-runs/${id}`),

  listLockHolders: (filters: Record<string, string | undefined> = {}) =>
    get<LockHolderListResponse>(`/api/control/lock-holders${qs(filters)}`),
  getLockHolder: (id: string) => get<LockHolderDetail>(`/api/control/lock-holders/${id}`),

  listEvents: (filters: Record<string, string | undefined> = {}, cursor?: string) =>
    get<EventListResponse>(`/api/control/events${qs({ ...filters, cursor })}`),

  getTrace: (executor: string, dispatchId: string) =>
    get<TraceResponse>(`/api/exec/${executor}/trace/${dispatchId}`),

  getClaim: (storeName: string, claimId: string) =>
    get<ClaimDetail>(`/api/store/${storeName}/claims/${claimId}`),
  listClaims: (storeName: string, filters: Record<string, string | undefined> = {}, cursor?: string) =>
    get<ClaimList>(`/api/store/${storeName}/claims${qs({ ...filters, cursor })}`),

  getAdminView: (storeName: string, viewName: string, params: Record<string, string> = {}) =>
    get<AdminViewResponse>(`/api/store/${storeName}/admin/${viewName}${qs(params)}`),

  // Parked-node diagnostics: surfaces every node currently in
  // phase='parked' across the cluster. ?reason= filters by snake_case
  // ParkReason value (time_wait | signal_wait | awaiting_human |
  // retry_backoff). Per 2026-05-14 Piece 2.
  listParkedNodes: (reason?: string) =>
    get<ParkedNodesResponse>(
      `/api/control/admin/diagnostics/parked-nodes${qs({ reason })}`,
    ),
};
