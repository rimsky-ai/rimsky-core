// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { StateBadge } from '../components/StateBadge';

export default function TemplateDetailPage() {
  const { hash = '' } = useParams();
  const { data, isLoading, error } = useQuery({
    queryKey: ['template', hash],
    queryFn: () => api.getTemplate(hash),
  });
  if (isLoading) return <p className="text-muted-foreground">Loading…</p>;
  if (error) return <p className="text-red-700">Error: {String(error)}</p>;
  if (!data) return null;
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="font-mono text-sm break-all">{data.template.id}</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-2 gap-2 text-sm">
            <dt className="text-muted-foreground">state</dt>
            <dd><StateBadge value={data.template.state} /></dd>
            <dt className="text-muted-foreground">registered</dt>
            <dd>{new Date(data.template.registered_at).toLocaleString()}</dd>
            <dt className="text-muted-foreground">source</dt>
            <dd>{data.template.source}</dd>
          </dl>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Tags</CardTitle>
        </CardHeader>
        <CardContent>
          {(data.tags ?? []).length === 0 && <p className="text-muted-foreground text-sm">No tags.</p>}
          {(data.tags ?? []).map((t) => (
            <div key={t.tag} className="font-mono text-sm py-1">{t.tag}</div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
