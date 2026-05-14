// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { TraceView } from '../components/TraceView';
import { CustomUIPanel } from '../components/CustomUIPanel';

export default function NodeRunDetailPage() {
  const { id = '' } = useParams();
  const [search] = useSearchParams();
  const executor = search.get('executor') ?? '';
  const { data, isLoading } = useQuery({
    queryKey: ['node-run', id],
    queryFn: () => api.getNodeRun(id),
  });
  // Resolve the executor's CustomUI declaration from its peer entry so
  // we can render the Custom UI panel with the per-spec §2.2
  // substitution markers ({dispatch_id}, {instance_id}, {node_type}).
  // The executor-detail page intentionally does not surface this
  // panel — those markers are only known on a per-node-run page.
  const executorName: string = executor || (data?.executor_name ?? '');
  const { data: executorPeer } = useQuery({
    queryKey: ['executor', executorName],
    queryFn: () => api.getExecutor(executorName),
    enabled: !!executorName,
  });
  const customUi = executorPeer?.peer.observability_capabilities?.custom_ui ?? null;
  const instanceId: string = data?.instance_id ?? '';
  const nodeType: string = data?.node_type ?? '';
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="font-mono text-sm break-all">Node-run {id}</CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading && <p className="text-muted-foreground">Loading…</p>}
          {data && (
            <pre className="text-xs whitespace-pre-wrap">{JSON.stringify(data, null, 2)}</pre>
          )}
        </CardContent>
      </Card>
      {executor && <TraceView executor={executor} dispatchId={id} />}
      {!executor && (
        <p className="text-sm text-muted-foreground">
          Add <code>?executor=NAME</code> to the URL to embed the trace pane.
        </p>
      )}
      {customUi && (
        <CustomUIPanel
          customUi={customUi}
          template={customUi.dispatch_url_template}
          substitutions={{
            dispatch_id: id,
            instance_id: instanceId,
            node_type: nodeType,
          }}
        />
      )}
    </div>
  );
}
