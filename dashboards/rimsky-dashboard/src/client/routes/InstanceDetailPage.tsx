// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { CascadeGraph } from '../components/CascadeGraph';
import { StateBadge } from '../components/StateBadge';

export default function InstanceDetailPage() {
  const { id = '' } = useParams();
  const { data, isLoading, error } = useQuery({
    queryKey: ['instance', id],
    queryFn: () => api.getInstance(id),
  });
  if (isLoading) return <p className="text-muted-foreground">Loading…</p>;
  if (error) return <p className="text-red-700">Error: {String(error)}</p>;
  if (!data) return null;
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="font-mono text-sm break-all">{data.instance.id}</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-2 gap-2 text-sm">
            <dt className="text-muted-foreground">instance_key</dt>
            <dd>{data.instance.instance_key ?? '—'}</dd>
            <dt className="text-muted-foreground">template</dt>
            <dd className="font-mono text-xs break-all">{data.instance.template_hash}</dd>
            <dt className="text-muted-foreground">state</dt>
            <dd><StateBadge value={data.instance.terminated_at ? 'completed' : 'active'} /></dd>
            <dt className="text-muted-foreground">created</dt>
            <dd>{new Date(data.instance.created_at).toLocaleString()}</dd>
            {data.instance.terminated_at && (
              <>
                <dt className="text-muted-foreground">terminated</dt>
                <dd>{new Date(data.instance.terminated_at).toLocaleString()}</dd>
              </>
            )}
          </dl>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Cascade graph</CardTitle>
        </CardHeader>
        <CardContent>
          <CascadeGraph nodes={data.cascade_graph ?? []} instanceId={data.instance.id} />
        </CardContent>
      </Card>
    </div>
  );
}
