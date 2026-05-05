// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { Badge } from './ui/badge';

const colorMap: Record<string, 'success' | 'warning' | 'error' | 'info' | 'muted'> = {
  // node states
  fresh: 'muted',
  stale: 'warning',
  running: 'info',
  failed: 'error',
  // frame states
  queued: 'warning',
  completed: 'success',
  // dispatch states
  pending: 'warning',
  claimed: 'info',
  // claim states
  open: 'info',
  OPEN: 'info',
  COMMITTED: 'success',
  ABANDONED: 'error',
  RELEASED: 'muted',
  UNKNOWN: 'muted',
  // peer reachability
  reachable: 'success',
  unreachable: 'error',
  degraded: 'warning',
  // template states
  registered: 'info',
  deployed: 'success',
  undeployed: 'muted',
  // active/inactive
  active: 'info',
};

export function StateBadge({ value }: { value?: string | null }) {
  if (!value) return <Badge variant="muted">—</Badge>;
  const v = colorMap[value] ?? 'default';
  return <Badge variant={v as any}>{value}</Badge>;
}
