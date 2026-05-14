// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { ParkedNodeEntry } from '../types';

// Snake_case ParkReason values (matching the proto enum's lower_snake_case
// projection). Per 2026-05-14 Piece 2 these are the valid filter values
// for the /admin/diagnostics/parked-nodes?reason= query.
const PARK_REASONS = [
  '',
  'time_wait',
  'signal_wait',
  'awaiting_human',
  'retry_backoff',
] as const;

export default function ParkedNodesPage() {
  const [reason, setReason] = useState<string>('');

  const { data, isLoading, error } = useQuery({
    queryKey: ['parked-nodes', reason],
    queryFn: () => api.listParkedNodes(reason === '' ? undefined : reason),
  });

  const rows: ParkedNodeEntry[] = data?.parked_nodes ?? [];
  const grouped = groupByReason(rows);

  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Parked nodes</h2>
      <div className="flex gap-2 items-end mb-4">
        <label className="flex flex-col text-sm">
          <span className="text-muted-foreground">reason</span>
          <select
            className="border rounded px-2 py-1"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          >
            {PARK_REASONS.map((r) => (
              <option key={r || 'all'} value={r}>
                {r === '' ? 'all' : r}
              </option>
            ))}
          </select>
        </label>
      </div>
      {isLoading && <p className="text-muted-foreground">Loading…</p>}
      {error && <p className="text-red-700">Error: {String(error)}</p>}
      {!isLoading && !error && rows.length === 0 && (
        <p className="text-muted-foreground">No parked nodes.</p>
      )}
      {Object.entries(grouped).map(([groupReason, items]) => (
        <ReasonGroup key={groupReason} reason={groupReason} entries={items} />
      ))}
    </div>
  );
}

function groupByReason(rows: ParkedNodeEntry[]): Record<string, ParkedNodeEntry[]> {
  const out: Record<string, ParkedNodeEntry[]> = {};
  for (const r of rows) {
    const key = r.reason ?? 'unspecified';
    if (!out[key]) out[key] = [];
    out[key]!.push(r);
  }
  return out;
}

function ReasonGroup({
  reason,
  entries,
}: {
  reason: string;
  entries: ParkedNodeEntry[];
}) {
  // awaiting_human is operator-attention; rows render with high-contrast
  // styling so they stand out at-a-glance on the dashboard.
  const operatorAttention = reason === 'awaiting_human';
  return (
    <div className="mb-6">
      <h3
        className={
          'text-lg font-medium mb-2 ' +
          (operatorAttention ? 'text-amber-700' : 'text-foreground')
        }
      >
        {reason}
        {operatorAttention && (
          <span className="ml-2 text-xs uppercase rounded bg-amber-100 text-amber-800 px-2 py-1">
            Operator attention
          </span>
        )}
      </h3>
      <table
        className={
          'w-full text-sm border ' +
          (operatorAttention ? 'border-amber-400' : 'border-muted')
        }
      >
        <thead>
          <tr className="border-b bg-muted">
            <th className="text-left px-3 py-2 font-medium">instance</th>
            <th className="text-left px-3 py-2 font-medium">node_id</th>
            <th className="text-left px-3 py-2 font-medium">parked_at</th>
            <th className="text-left px-3 py-2 font-medium">resume_at</th>
            <th className="text-left px-3 py-2 font-medium">reason_note</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((e) => (
            <tr
              key={e.node_id}
              className={
                'border-b ' +
                (operatorAttention
                  ? 'bg-amber-50/40 hover:bg-amber-50/80'
                  : 'hover:bg-muted/30')
              }
            >
              <td className="px-3 py-2 font-mono text-xs">
                {e.instance_id.slice(0, 8)}…
              </td>
              <td className="px-3 py-2 font-mono text-xs">
                {e.node_id.slice(0, 8)}…
              </td>
              <td className="px-3 py-2 font-mono text-xs">
                {new Date(e.parked_at).toLocaleString()}
              </td>
              <td className="px-3 py-2 font-mono text-xs">
                {e.resume_at ? new Date(e.resume_at).toLocaleString() : '—'}
              </td>
              <td className="px-3 py-2 text-xs">{e.reason_note ?? ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
