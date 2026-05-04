import { api } from '../api';
import { ResourceTable } from '../components/ResourceTable';
import { StateBadge } from '../components/StateBadge';
import type { FrameRow } from '../types';

export default function FramesPage() {
  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Frames</h2>
      <ResourceTable<FrameRow>
        queryKey={['frames']}
        fetchPage={async (cursor) => {
          const r = await api.listFrames({}, cursor);
          return { rows: r.frames, next_cursor: r.next_cursor };
        }}
        rowLink={(r) => `/frames/${r.frame_id}`}
        columns={[
          { header: 'id', accessor: (r) => <span className="font-mono text-xs">{r.frame_id.slice(0, 8)}…</span> },
          { header: 'instance', accessor: (r) => <span className="font-mono text-xs">{r.instance_id.slice(0, 8)}…</span> },
          { header: 'state', accessor: (r) => <StateBadge value={r.state} /> },
          { header: 'mode', accessor: (r) => r.mode },
          { header: 'started', accessor: (r) => r.started_at ? new Date(r.started_at).toLocaleString() : '—' },
        ]}
      />
    </div>
  );
}
