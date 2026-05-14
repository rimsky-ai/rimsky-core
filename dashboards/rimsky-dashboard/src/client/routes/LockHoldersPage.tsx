// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { Button } from '../components/ui/button';
import { Link } from 'react-router-dom';

export default function LockHoldersPage() {
  const [producerName, setProducerName] = useState('');
  const [holderNodeId, setHolderNodeId] = useState('');
  const [supId, setSupId] = useState('');
  const [instanceId, setInstanceId] = useState('');
  const [nodeType, setNodeType] = useState('');
  const { data, isLoading, error } = useQuery({
    queryKey: ['lock-holders', producerName, holderNodeId, supId, instanceId, nodeType],
    queryFn: () => {
      const filters: Record<string, string> = {};
      if (producerName) filters.producer_name = producerName;
      if (holderNodeId) filters.holder_node_id = holderNodeId;
      if (supId) filters.holder_supervisor_id = supId;
      if (instanceId) filters.instance_id = instanceId;
      if (nodeType) filters.node_type = nodeType;
      return api.listLockHolders(filters);
    },
  });
  return (
    <div className="space-y-4">
      <h2 className="text-xl font-semibold">Lock holders</h2>
      <Card>
        <CardHeader><CardTitle>Filter</CardTitle></CardHeader>
        <CardContent>
          <p className="text-xs text-muted-foreground mb-2">
            All filters are optional; leaving them blank lists every live lock-holder.
          </p>
          <div className="grid grid-cols-2 gap-2">
            <input
              className="border rounded px-2 py-1 text-sm"
              placeholder="producer_name"
              value={producerName}
              onChange={(e) => setProducerName(e.target.value)}
            />
            <input
              className="border rounded px-2 py-1 text-sm"
              placeholder="instance_id (UUID)"
              value={instanceId}
              onChange={(e) => setInstanceId(e.target.value)}
            />
            <input
              className="border rounded px-2 py-1 text-sm"
              placeholder="node_type"
              value={nodeType}
              onChange={(e) => setNodeType(e.target.value)}
            />
            <input
              className="border rounded px-2 py-1 text-sm"
              placeholder="holder_node_id (UUID)"
              value={holderNodeId}
              onChange={(e) => setHolderNodeId(e.target.value)}
            />
            <input
              className="border rounded px-2 py-1 text-sm"
              placeholder="holder_supervisor_id"
              value={supId}
              onChange={(e) => setSupId(e.target.value)}
            />
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardContent className="p-0">
          {isLoading && <p className="p-4 text-muted-foreground">Loading…</p>}
          {error && <p className="p-4 text-red-700">Error: {String(error)}</p>}
          {data && (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b bg-muted">
                  <th className="text-left px-3 py-2">claim_id</th>
                  <th className="text-left px-3 py-2">kind</th>
                  <th className="text-left px-3 py-2">store</th>
                  <th className="text-left px-3 py-2">name</th>
                  <th className="text-left px-3 py-2">claimed_at</th>
                </tr>
              </thead>
              <tbody>
                {(data.lock_holders ?? []).length === 0 && (
                  <tr>
                    <td colSpan={5} className="px-3 py-6 text-center text-muted-foreground">No matching lock holders.</td>
                  </tr>
                )}
                {(data.lock_holders ?? []).map((r) => (
                  <tr key={r.claim_id} className="border-b hover:bg-muted/30">
                    <td className="px-3 py-2"><Link to={`/lock-holders/${r.claim_id}`} className="hover:underline font-mono text-xs">{r.claim_id.slice(0, 8)}…</Link></td>
                    <td className="px-3 py-2">{r.lock_kind}</td>
                    <td className="px-3 py-2">{r.producer_name ?? '—'}</td>
                    <td className="px-3 py-2 font-mono text-xs">{r.lock_name ?? '—'}</td>
                    <td className="px-3 py-2">{new Date(r.claimed_at).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </CardContent>
      </Card>
      <Button variant="ghost" size="sm" onClick={() => {
        setProducerName('');
        setHolderNodeId('');
        setSupId('');
        setInstanceId('');
        setNodeType('');
      }}>
        Clear filters
      </Button>
    </div>
  );
}
