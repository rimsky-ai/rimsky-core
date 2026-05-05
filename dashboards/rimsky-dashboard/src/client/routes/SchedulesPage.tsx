// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { api } from '../api';
import { ResourceTable } from '../components/ResourceTable';
import type { ScheduleRow } from '../types';

export default function SchedulesPage() {
  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Schedules</h2>
      <ResourceTable<ScheduleRow>
        queryKey={['schedules']}
        fetchPage={async (cursor) => {
          const r = await api.listSchedules(cursor);
          return { rows: r.schedules ?? [], next_cursor: r.next_cursor ?? '' };
        }}
        emptyMessage="No schedules."
        columns={[
          {
            header: 'node_id',
            accessor: (s) => (
              <span className="font-mono text-xs">{s.node_id.slice(0, 8)}…</span>
            ),
          },
          {
            header: 'cron',
            accessor: (s) => <span className="font-mono text-xs">{s.cron_expr}</span>,
          },
          {
            header: 'next fire',
            accessor: (s) => new Date(s.next_fire_at).toLocaleString(),
          },
          {
            header: 'last fired',
            accessor: (s) =>
              s.last_fired_at ? new Date(s.last_fired_at).toLocaleString() : '—',
          },
        ]}
      />
    </div>
  );
}
