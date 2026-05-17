// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// AssetDetailPage — per-asset detail panel. Shows current version,
// version history, materialization history, lineage walks (upstream +
// downstream stub), plus Materialize / Delete operator actions.
//
// @concept: asset

import { useParams } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { api } from '../../api';

export default function AssetDetailPage() {
  const params = useParams<{ instanceId: string; alias: string }>();
  const instanceId = params.instanceId ?? '';
  const alias = params.alias ?? '';
  const queryClient = useQueryClient();

  const detailQuery = useQuery({
    queryKey: ['asset', instanceId, alias],
    queryFn: () => api.getAsset(instanceId, alias),
    enabled: Boolean(instanceId && alias),
  });

  const [actionError, setActionError] = useState<string | null>(null);
  const [actionStatus, setActionStatus] = useState<string | null>(null);

  const materializeMu = useMutation({
    mutationFn: () => api.materializeAsset(instanceId, alias),
    onSuccess: () => {
      setActionStatus('materialize: queued');
      setActionError(null);
      queryClient.invalidateQueries({ queryKey: ['asset', instanceId, alias] });
    },
    onError: (err) => {
      setActionStatus(null);
      setActionError((err as Error).message);
    },
  });

  const deleteMu = useMutation({
    mutationFn: () => api.deleteAsset(instanceId, alias),
    onSuccess: () => {
      setActionStatus('delete: ok');
      setActionError(null);
      queryClient.invalidateQueries({ queryKey: ['asset', instanceId, alias] });
    },
    onError: (err) => {
      setActionStatus(null);
      setActionError((err as Error).message);
    },
  });

  if (detailQuery.isLoading) {
    return <div className="text-sm">Loading…</div>;
  }
  if (detailQuery.isError) {
    return (
      <div className="text-sm text-destructive">
        Failed to load: {(detailQuery.error as Error).message}
      </div>
    );
  }
  const detail = detailQuery.data;
  if (!detail) {
    return <div className="text-sm">No data.</div>;
  }

  return (
    <div>
      <h2 className="text-xl font-semibold mb-2">Asset: {alias}</h2>
      <div className="text-sm text-muted-foreground mb-4">
        Instance <code>{instanceId.slice(0, 8)}…</code> · producer{' '}
        <code>{detail.asset.producer_name}</code> · scope_data_hash{' '}
        <code className="text-xs">{detail.asset.scope_data_hash}</code>
      </div>

      <div className="mb-4 flex items-center gap-2">
        <button
          className="px-3 py-1 rounded bg-primary text-primary-foreground text-sm"
          onClick={() => materializeMu.mutate()}
          disabled={materializeMu.isPending}
        >
          Materialize
        </button>
        <button
          className="px-3 py-1 rounded bg-destructive text-destructive-foreground text-sm"
          onClick={() => {
            if (window.confirm(`Delete asset ${alias}? This calls Release on the claim handle.`)) {
              deleteMu.mutate();
            }
          }}
          disabled={deleteMu.isPending}
        >
          Delete
        </button>
        {actionStatus && <span className="text-sm text-muted-foreground">{actionStatus}</span>}
        {actionError && <span className="text-sm text-destructive">{actionError}</span>}
      </div>

      <section className="mb-6">
        <h3 className="text-lg font-semibold mb-2">Current version</h3>
        <div className="text-sm">
          <code>{detail.asset.current_version_id ?? '—'}</code>
        </div>
      </section>

      <section className="mb-6">
        <h3 className="text-lg font-semibold mb-2">Version history</h3>
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left border-b">
              <th className="py-2 px-3">version_id</th>
              <th className="py-2 px-3">committed_at</th>
            </tr>
          </thead>
          <tbody>
            {detail.versions.map((v) => (
              <tr key={v.version_id} className="border-b">
                <td className="py-2 px-3 font-mono text-xs">{v.version_id}</td>
                <td className="py-2 px-3">{new Date(v.committed_at).toLocaleString()}</td>
              </tr>
            ))}
            {detail.versions.length === 0 && (
              <tr>
                <td colSpan={2} className="py-2 px-3 text-center text-muted-foreground">
                  No versions committed yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </section>

      <section className="mb-6">
        <h3 className="text-lg font-semibold mb-2">Materialization history</h3>
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left border-b">
              <th className="py-2 px-3">version_id</th>
              <th className="py-2 px-3">parent_run_id</th>
              <th className="py-2 px-3">frame_id</th>
              <th className="py-2 px-3">committed_at</th>
            </tr>
          </thead>
          <tbody>
            {detail.materializations.map((m) => (
              <tr key={m.version_id} className="border-b">
                <td className="py-2 px-3 font-mono text-xs">{m.version_id}</td>
                <td className="py-2 px-3 font-mono text-xs">
                  {m.parent_run_id ?? '—'}
                </td>
                <td className="py-2 px-3 font-mono text-xs">{m.frame_id ?? '—'}</td>
                <td className="py-2 px-3">{new Date(m.committed_at).toLocaleString()}</td>
              </tr>
            ))}
            {detail.materializations.length === 0 && (
              <tr>
                <td colSpan={4} className="py-2 px-3 text-center text-muted-foreground">
                  No materializations recorded.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </section>

      <section className="mb-6">
        <h3 className="text-lg font-semibold mb-2">Lineage walks</h3>
        <div className="grid grid-cols-2 gap-4">
          <div>
            <div className="font-medium text-sm mb-1">Upstream</div>
            <ul className="text-sm">
              {(detail.upstream ?? []).map((edge, idx) => (
                <li key={idx} className="font-mono text-xs">
                  {edge.kind} {edge.run_id ?? edge.claim_handle_id ?? '—'}
                </li>
              ))}
              {(!detail.upstream || detail.upstream.length === 0) && (
                <li className="text-muted-foreground">No upstream edges.</li>
              )}
            </ul>
          </div>
          <div>
            <div className="font-medium text-sm mb-1">Downstream</div>
            <ul className="text-sm">
              {(detail.downstream ?? []).map((edge, idx) => (
                <li key={idx} className="font-mono text-xs">
                  {edge.kind} {edge.run_id ?? edge.claim_handle_id ?? '—'}
                </li>
              ))}
              {(!detail.downstream || detail.downstream.length === 0) && (
                <li className="text-muted-foreground">No downstream edges.</li>
              )}
            </ul>
          </div>
        </div>
      </section>
    </div>
  );
}
