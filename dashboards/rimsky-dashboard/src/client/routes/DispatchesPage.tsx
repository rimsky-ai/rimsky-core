// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { api } from '../api';
import { ResourceTable } from '../components/ResourceTable';
import { StateBadge } from '../components/StateBadge';
import type { DispatchRow } from '../types';

export default function DispatchesPage() {
  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Dispatches (live)</h2>
      <ResourceTable<DispatchRow>
        queryKey={['dispatches']}
        fetchPage={async (cursor) => {
          const r = await api.listDispatches({}, cursor);
          return { rows: r.dispatches, next_cursor: r.next_cursor };
        }}
        rowLink={(r) => `/dispatches/${r.id}`}
        columns={[
          { header: 'id', accessor: (r) => <span className="font-mono text-xs">{r.id.slice(0, 8)}…</span> },
          { header: 'state', accessor: (r) => <StateBadge value={r.state} /> },
          { header: 'executor', accessor: (r) => r.executor_name ?? '—' },
          { header: 'claimed_by', accessor: (r) => <span className="font-mono text-xs">{r.claimed_by ?? '—'}</span> },
          { header: 'enqueued', accessor: (r) => new Date(r.enqueued_at).toLocaleString() },
        ]}
      />
    </div>
  );
}
