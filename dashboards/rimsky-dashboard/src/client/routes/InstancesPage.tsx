// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { api } from '../api';
import { ResourceTable } from '../components/ResourceTable';
import { StateBadge } from '../components/StateBadge';
import type { InstanceRow } from '../types';

export default function InstancesPage() {
  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Instances</h2>
      <ResourceTable<InstanceRow>
        queryKey={['instances']}
        fetchPage={async (cursor) => {
          const r = await api.listInstances({}, cursor);
          return { rows: r.instances, next_cursor: r.next_cursor };
        }}
        rowLink={(r) => `/instances/${r.id}`}
        columns={[
          { header: 'id', accessor: (r) => <span className="font-mono text-xs">{r.id.slice(0, 8)}…</span> },
          { header: 'instance_key', accessor: (r) => r.instance_key ?? '—' },
          {
            header: 'state',
            accessor: (r) => <StateBadge value={r.terminated_at ? 'completed' : 'active'} />,
          },
          { header: 'template', accessor: (r) => <span className="font-mono text-xs">{r.template_hash.slice(0, 16)}…</span> },
          { header: 'created', accessor: (r) => new Date(r.created_at).toLocaleString() },
        ]}
      />
    </div>
  );
}
