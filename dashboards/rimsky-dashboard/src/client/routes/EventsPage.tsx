import { useState } from 'react';
import { api } from '../api';
import { ResourceTable } from '../components/ResourceTable';
import { Card, CardContent } from '../components/ui/card';
import { Button } from '../components/ui/button';
import type { EventRow } from '../types';

export default function EventsPage() {
  const [filters, setFilters] = useState<{ kind?: string; instance_id?: string; node_id?: string }>({});
  const [draft, setDraft] = useState(filters);
  return (
    <div className="space-y-4">
      <h2 className="text-xl font-semibold">Events</h2>
      <Card>
        <CardContent className="p-3">
          <div className="flex gap-2">
            <input
              className="border rounded px-2 py-1 text-sm flex-1"
              placeholder="kind"
              value={draft.kind ?? ''}
              onChange={(e) => setDraft({ ...draft, kind: e.target.value })}
            />
            <input
              className="border rounded px-2 py-1 text-sm flex-1"
              placeholder="instance_id (UUID)"
              value={draft.instance_id ?? ''}
              onChange={(e) => setDraft({ ...draft, instance_id: e.target.value })}
            />
            <input
              className="border rounded px-2 py-1 text-sm flex-1"
              placeholder="node_id (UUID)"
              value={draft.node_id ?? ''}
              onChange={(e) => setDraft({ ...draft, node_id: e.target.value })}
            />
            <Button size="sm" onClick={() => setFilters(draft)}>Apply</Button>
            <Button variant="ghost" size="sm" onClick={() => { setDraft({}); setFilters({}); }}>Clear</Button>
          </div>
        </CardContent>
      </Card>
      <ResourceTable<EventRow>
        queryKey={['events', filters]}
        fetchPage={async (cursor) => {
          const r = await api.listEvents(filters, cursor);
          return { rows: r.events, next_cursor: r.next_cursor };
        }}
        columns={[
          { header: 'occurred', accessor: (r) => new Date(r.occurred_at).toLocaleString() },
          { header: 'kind', accessor: (r) => r.kind },
          { header: 'instance', accessor: (r) => <span className="font-mono text-xs">{r.instance_id?.slice(0, 8) ?? '—'}…</span> },
          { header: 'node', accessor: (r) => <span className="font-mono text-xs">{r.node_id?.slice(0, 8) ?? '—'}…</span> },
          { header: 'payload', accessor: (r) => <span className="font-mono text-xs">{JSON.stringify(r.payload).slice(0, 100)}</span> },
        ]}
      />
    </div>
  );
}
