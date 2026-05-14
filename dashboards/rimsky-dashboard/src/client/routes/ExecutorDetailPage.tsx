// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { Card, CardContent } from '../components/ui/card';
import { Tabs } from '../components/ui/tabs';
import { StateBadge } from '../components/StateBadge';

export default function ExecutorDetailPage() {
  const { name = '' } = useParams();
  const { data, isLoading, error } = useQuery({ queryKey: ['executor', name], queryFn: () => api.getExecutor(name) });
  if (isLoading) return <p className="text-muted-foreground">Loading…</p>;
  if (error) return <p className="text-red-700">Error: {String(error)}</p>;
  if (!data) return null;
  const peer = data.peer;
  const caps = peer.observability_capabilities;
  // CustomUIPanel is intentionally not surfaced here. Per spec §2.2 the
  // executor `dispatch_url_template` substitution markers are
  // {dispatch_id} / {instance_id} / {node_type} — none of which are
  // known on a peer-detail page. The Custom UI surface lives on
  // NodeRunDetailPage, where those values are in scope.
  const tabs: { id: string; label: string; content: React.ReactNode }[] = [
    {
      id: 'overview',
      label: 'Overview',
      content: (
        <Card>
          <CardContent className="p-4">
            <dl className="grid grid-cols-2 gap-2 text-sm">
              <dt className="text-muted-foreground">endpoint</dt>
              <dd className="font-mono text-xs">{peer.endpoint}</dd>
              <dt className="text-muted-foreground">observability_endpoint</dt>
              <dd className="font-mono text-xs">{peer.observability_endpoint}</dd>
              <dt className="text-muted-foreground">reachability</dt>
              <dd><StateBadge value={peer.reachability_status} /></dd>
              {peer.last_error && (
                <>
                  <dt className="text-muted-foreground">last_error</dt>
                  <dd className="text-red-700 text-xs">{peer.last_error}</dd>
                </>
              )}
            </dl>
            {caps && (
              <div className="mt-3 text-xs text-muted-foreground">
                retention {caps.retention_after_terminal_seconds}s ·
                {caps.supports_trace_get && ' trace_get'}
                {caps.supports_trace_stream && ' trace_stream'}
              </div>
            )}
            {caps?.custom_ui && (
              <p className="mt-3 text-xs text-muted-foreground">
                Custom UI is configured for this executor. Markers like
                <code className="mx-1">{'{dispatch_id}'}</code> are
                resolved per-dispatch, so the embedded UI is rendered
                from individual dispatch pages.
              </p>
            )}
          </CardContent>
        </Card>
      ),
    },
  ];
  return (
    <div className="space-y-4">
      <h2 className="text-xl font-semibold">Executor: {peer.name}</h2>
      <Tabs tabs={tabs} />
    </div>
  );
}
