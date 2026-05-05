// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import type { ObservabilityCapabilities, PeerEntry } from '../types';

export function hasTraceGet(p: PeerEntry | null | undefined): boolean {
  return !!p?.observability_capabilities?.supports_trace_get;
}
export function hasTraceStream(p: PeerEntry | null | undefined): boolean {
  return !!p?.observability_capabilities?.supports_trace_stream;
}
export function hasClaimGet(p: PeerEntry | null | undefined): boolean {
  return !!p?.observability_capabilities?.supports_claim_get;
}
export function hasListClaims(p: PeerEntry | null | undefined): boolean {
  return !!p?.observability_capabilities?.supports_list_claims;
}
export function adminViews(p: PeerEntry | null | undefined) {
  return p?.observability_capabilities?.admin_views ?? [];
}
export function customUI(p: PeerEntry | null | undefined) {
  return p?.observability_capabilities?.custom_ui ?? null;
}
export function retentionSeconds(c: ObservabilityCapabilities | null | undefined): number {
  return c?.retention_after_terminal_seconds ?? 0;
}
