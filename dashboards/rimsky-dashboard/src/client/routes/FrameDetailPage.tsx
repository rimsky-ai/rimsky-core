// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useParams, Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { StateBadge } from '../components/StateBadge';

export default function FrameDetailPage() {
  const { id = '' } = useParams();
  const { data, isLoading, error } = useQuery({
    queryKey: ['frame', id],
    queryFn: () => api.getFrame(id),
  });
  if (isLoading) return <p className="text-muted-foreground">Loading…</p>;
  if (error) return <p className="text-red-700">Error: {String(error)}</p>;
  if (!data) return null;
  const f = data.frame;
  return (
    <Card>
      <CardHeader>
        <CardTitle className="font-mono text-sm break-all">{f.frame_id}</CardTitle>
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-muted-foreground">state</dt>
          <dd><StateBadge value={f.state} /></dd>
          <dt className="text-muted-foreground">mode</dt>
          <dd>{f.mode}</dd>
          <dt className="text-muted-foreground">instance</dt>
          <dd><Link className="hover:underline font-mono text-xs" to={`/instances/${f.instance_id}`}>{f.instance_id}</Link></dd>
          <dt className="text-muted-foreground">started</dt>
          <dd>{f.started_at ? new Date(f.started_at).toLocaleString() : '—'}</dd>
          <dt className="text-muted-foreground">ended</dt>
          <dd>{f.ended_at ? new Date(f.ended_at).toLocaleString() : '—'}</dd>
          <dt className="text-muted-foreground">timeout</dt>
          <dd>{f.frame_timeout_ms}ms</dd>
        </dl>
      </CardContent>
    </Card>
  );
}
