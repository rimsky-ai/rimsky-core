// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// AssetsPage — top-level cross-instance asset list. Per the 2026-05-15
// data-platform-extensions spec §Lifetime and the asset pattern, assets
// are the documented compound: claim against a `DataProcessing`-capable
// producer + `lifetime: durable`. The current control-api shape is
// per-instance (`GET /instances/{id}/assets`); this page picks the
// instance via dropdown and surfaces the rows.
//
// @concept: asset

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';

import { api } from '../../api';
import type { AssetRow, InstanceRow } from '../../types';

export default function AssetsPage() {
  const instancesQuery = useQuery({
    queryKey: ['instances', 'for-assets'],
    queryFn: () => api.listInstances({}),
  });
  const instances: InstanceRow[] = instancesQuery.data?.instances ?? [];
  const [instanceId, setInstanceId] = useState<string>('');
  const effectiveInstance = instanceId || instances[0]?.id || '';

  const assetsQuery = useQuery({
    queryKey: ['assets', effectiveInstance],
    queryFn: () => api.listAssets(effectiveInstance),
    enabled: Boolean(effectiveInstance),
  });

  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Assets</h2>
      <p className="text-sm text-muted-foreground mb-4">
        Assets are claims with <code>lifetime: durable</code> against a
        DataProcessing-capable producer. Surfaced via the
        <code> /instances/&#123;id&#125;/assets/...</code> control-api family.
      </p>
      <div className="mb-4 flex items-center gap-2">
        <label className="text-sm">Instance:</label>
        <select
          className="border rounded px-2 py-1 text-sm bg-background"
          value={effectiveInstance}
          onChange={(e) => setInstanceId(e.target.value)}
        >
          {instances.map((inst) => (
            <option key={inst.id} value={inst.id}>
              {inst.instance_key ?? inst.id.slice(0, 8) + '…'}
            </option>
          ))}
        </select>
      </div>
      {assetsQuery.isLoading && <div className="text-sm">Loading…</div>}
      {assetsQuery.isError && (
        <div className="text-sm text-destructive">
          Failed to load assets: {(assetsQuery.error as Error).message}
        </div>
      )}
      {assetsQuery.data && (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left border-b">
              <th className="py-2 px-3">alias</th>
              <th className="py-2 px-3">producer</th>
              <th className="py-2 px-3">version</th>
              <th className="py-2 px-3">state</th>
              <th className="py-2 px-3">lifetime</th>
              <th className="py-2 px-3">claimed</th>
            </tr>
          </thead>
          <tbody>
            {assetsQuery.data.assets.map((row: AssetRow) => (
              <tr key={`${effectiveInstance}/${row.alias}`} className="border-b hover:bg-accent">
                <td className="py-2 px-3">
                  <Link
                    to={`/instances/${effectiveInstance}/assets/${row.alias}`}
                    className="text-primary underline"
                  >
                    {row.alias}
                  </Link>
                </td>
                <td className="py-2 px-3 font-mono text-xs">{row.producer_name}</td>
                <td className="py-2 px-3 font-mono text-xs">
                  {row.version_id ?? '—'}
                </td>
                <td className="py-2 px-3">{row.state}</td>
                <td className="py-2 px-3">{row.lifetime}</td>
                <td className="py-2 px-3">{new Date(row.claimed_at).toLocaleString()}</td>
              </tr>
            ))}
            {assetsQuery.data.assets.length === 0 && (
              <tr>
                <td colSpan={6} className="py-4 px-3 text-center text-muted-foreground">
                  No assets in this instance.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      )}
    </div>
  );
}
