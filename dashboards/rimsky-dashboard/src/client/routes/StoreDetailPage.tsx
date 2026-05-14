// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { Tabs } from '../components/ui/tabs';
import { StateBadge } from '../components/StateBadge';
import { AdminView } from '../components/AdminView';
import { ClaimView } from '../components/ClaimView';
import { CustomUIPanel } from '../components/CustomUIPanel';

export default function StoreDetailPage() {
  const { name = '' } = useParams();
  const [search] = useSearchParams();
  const claimId = search.get('claim') ?? '';
  const { data, isLoading, error } = useQuery({ queryKey: ['store', name], queryFn: () => api.getStore(name) });
  if (isLoading) return <p className="text-muted-foreground">Loading…</p>;
  if (error) return <p className="text-red-700">Error: {String(error)}</p>;
  if (!data) return null;
  const peer = data.peer;
  const caps = peer.observability_capabilities;
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
                {caps.supports_claim_get && ' claim_get'}
                {caps.supports_claim_stream && ' claim_stream'}
                {caps.supports_list_claims && ' list_claims'}
              </div>
            )}
          </CardContent>
        </Card>
      ),
    },
  ];
  if (caps?.supports_list_claims) {
    tabs.push({ id: 'claims', label: 'Claims', content: <ClaimsTab storeName={name} /> });
  }
  if (claimId && caps?.supports_claim_get) {
    tabs.push({ id: 'claim', label: `Claim ${claimId.slice(0, 8)}`, content: <ClaimView storeName={name} claimId={claimId} /> });
  }
  for (const v of caps?.admin_views ?? []) {
    tabs.push({ id: `admin-${v.name}`, label: v.title, content: <AdminView storeName={name} decl={v} /> });
  }
  if (caps?.custom_ui) {
    tabs.push({
      id: 'customUi',
      label: 'Custom UI',
      content: (
        <CustomUIPanel
          customUi={caps.custom_ui}
          template={caps.custom_ui.dispatch_url_template}
          substitutions={{ producer_name: name, claim_id: claimId }}
        />
      ),
    });
  }

  return (
    <div className="space-y-4">
      <h2 className="text-xl font-semibold">Store: {peer.name}</h2>
      <Tabs tabs={tabs} defaultTab={claimId ? 'claim' : undefined} />
    </div>
  );
}

function ClaimsTab({ storeName }: { storeName: string }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['claims', storeName],
    queryFn: () => api.listClaims(storeName),
  });
  if (isLoading) return <p className="text-muted-foreground">Loading…</p>;
  if (error) return <p className="text-red-700">Error: {String(error)}</p>;
  return (
    <Card>
      <CardHeader><CardTitle>Recent claims</CardTitle></CardHeader>
      <CardContent>
        {(data?.claims ?? []).length === 0 && <p className="text-muted-foreground text-sm">No claims.</p>}
        {(data?.claims ?? []).map((c) => (
          <div key={c.claim_id} className="flex items-center gap-2 py-1 text-sm">
            <a href={`/stores/${storeName}?claim=${c.claim_id}`} className="hover:underline font-mono text-xs">
              {c.claim_id}
            </a>
            <StateBadge value={c.state} />
            {c.opened_at && <span className="text-xs text-muted-foreground">{new Date(c.opened_at).toLocaleString()}</span>}
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
