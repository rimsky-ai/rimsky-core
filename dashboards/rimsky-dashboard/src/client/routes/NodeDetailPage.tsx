// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useParams, Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { StateBadge } from '../components/StateBadge';

export default function NodeDetailPage() {
  const { instanceId = '', nodeType = '' } = useParams();
  const { data, isLoading, error } = useQuery({
    queryKey: ['node', instanceId, nodeType],
    queryFn: () => api.getNode(instanceId, nodeType),
  });
  if (isLoading) return <p className="text-muted-foreground">Loading…</p>;
  if (error) return <p className="text-red-700">Error: {String(error)}</p>;
  if (!data) return null;
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>{data.node.node_type}</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-2 gap-2 text-sm">
            <dt className="text-muted-foreground">state</dt>
            <dd><StateBadge value={data.node.state} /></dd>
            <dt className="text-muted-foreground">retry</dt>
            <dd>{data.node.retry_counter}</dd>
            <dt className="text-muted-foreground">error class</dt>
            <dd>{data.node.current_error_class ?? '—'}</dd>
            <dt className="text-muted-foreground">executor</dt>
            <dd>{data.node.executor || '—'}</dd>
            <dt className="text-muted-foreground">last heartbeat</dt>
            <dd>{data.node.last_heartbeat_at ? new Date(data.node.last_heartbeat_at).toLocaleString() : '—'}</dd>
            <dt className="text-muted-foreground">supervisor</dt>
            <dd className="font-mono text-xs">{data.node.assigned_supervisor_id || '—'}</dd>
          </dl>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Recent events</CardTitle>
        </CardHeader>
        <CardContent>
          {(data.events ?? []).length === 0 && <p className="text-muted-foreground text-sm">No events.</p>}
          {(data.events ?? []).map((ev) => (
            <div key={ev.id} className="flex items-center gap-2 py-1 text-sm border-b">
              <span className="text-muted-foreground tabular-nums">{new Date(ev.occurred_at).toLocaleTimeString()}</span>
              <span className="font-medium">{ev.kind}</span>
              <span className="text-muted-foreground text-xs">{JSON.stringify(ev.payload)}</span>
            </div>
          ))}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Current claim holdings</CardTitle>
        </CardHeader>
        <CardContent>
          {(data.holdings ?? []).length === 0 && <p className="text-muted-foreground text-sm">No active claims.</p>}
          {(data.holdings ?? []).map((h) => (
            <div key={h.claim_id} className="text-sm py-1">
              <Link to={`/lock-holders/${h.claim_id}`} className="hover:underline font-mono text-xs">{h.claim_id}</Link>
              <span className="text-muted-foreground ml-2">{h.lock_kind}</span>
              {h.lock_name && <span className="text-muted-foreground ml-2">{h.lock_name}</span>}
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
