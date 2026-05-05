// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { api } from '../api';
import { ResourceTable } from '../components/ResourceTable';
import { StateBadge } from '../components/StateBadge';
import type { TemplateRow } from '../types';

export default function TemplatesPage() {
  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Templates</h2>
      <ResourceTable<TemplateRow>
        queryKey={['templates']}
        fetchPage={async (cursor) => {
          const r = await api.listTemplates({}, cursor);
          return { rows: r.templates, next_cursor: r.next_cursor };
        }}
        rowLink={(r) => `/templates/${r.id}`}
        columns={[
          { header: 'hash', accessor: (r) => <span className="font-mono text-xs">{r.id.slice(0, 16)}…</span> },
          { header: 'state', accessor: (r) => <StateBadge value={r.state} /> },
          { header: 'source', accessor: (r) => r.source },
          { header: 'registered', accessor: (r) => new Date(r.registered_at).toLocaleString() },
        ]}
      />
    </div>
  );
}
