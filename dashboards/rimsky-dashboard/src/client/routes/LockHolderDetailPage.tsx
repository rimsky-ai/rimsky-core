// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { Link, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { StateBadge } from '../components/StateBadge';

export default function LockHolderDetailPage() {
  const { id = '' } = useParams();
  const { data, isLoading, error } = useQuery({
    queryKey: ['lock-holder', id],
    queryFn: () => api.getLockHolder(id),
  });
  if (isLoading) return <p className="text-muted-foreground">Loading…</p>;
  if (error) return <p className="text-red-700">Error: {String(error)}</p>;
  if (!data) return null;
  const r = data.lock_holder;
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="font-mono text-sm break-all">{r.claim_id}</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-2 gap-2 text-sm">
            <dt className="text-muted-foreground">kind</dt>
            <dd>{r.lock_kind}</dd>
            <dt className="text-muted-foreground">name</dt>
            <dd className="font-mono text-xs">{r.lock_name ?? '—'}</dd>
            <dt className="text-muted-foreground">store</dt>
            <dd>{r.producer_name ? <Link to={`/stores/${r.producer_name}`} className="hover:underline">{r.producer_name}</Link> : '—'}</dd>
            <dt className="text-muted-foreground">supervisor</dt>
            <dd className="font-mono text-xs">{r.holder_supervisor_id}</dd>
            <dt className="text-muted-foreground">node</dt>
            <dd className="font-mono text-xs">{r.holder_node_id}</dd>
            <dt className="text-muted-foreground">claimed_at</dt>
            <dd>{new Date(r.claimed_at).toLocaleString()}</dd>
            <dt className="text-muted-foreground">expires_at</dt>
            <dd>{new Date(r.expires_at).toLocaleString()}</dd>
            <dt className="text-muted-foreground">frame_id</dt>
            <dd className="font-mono text-xs">{r.frame_id ?? '—'}</dd>
          </dl>
          {r.producer_name && (
            <p className="mt-3 text-sm">
              <Link className="underline" to={`/stores/${r.producer_name}?claim=${r.claim_id}`}>
                View claim on {r.producer_name} →
              </Link>
            </p>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Held subgraph</CardTitle>
        </CardHeader>
        <CardContent>
          {(data.claim_holders ?? []).length === 0 && <p className="text-muted-foreground text-sm">No claim-holders.</p>}
          {(data.claim_holders ?? []).map((c) => (
            <div key={c.id} className="flex items-center gap-2 py-1 text-sm">
              <span className="font-mono text-xs">{c.holder_node_id.slice(0, 8)}…</span>
              <StateBadge value={c.state} />
              {c.completed_at && <span className="text-muted-foreground text-xs">{new Date(c.completed_at).toLocaleString()}</span>}
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
