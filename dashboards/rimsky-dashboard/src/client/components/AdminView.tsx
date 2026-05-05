// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { AdminViewDecl } from '../types';
import { Button } from './ui/button';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card';

export function AdminView({ storeName, decl }: { storeName: string; decl: AdminViewDecl }) {
  const [params, setParams] = useState<Record<string, string>>({});
  const [submitted, setSubmitted] = useState<Record<string, string> | null>(null);

  const { data, isLoading, error } = useQuery({
    queryKey: ['admin-view', storeName, decl.name, submitted],
    queryFn: () => api.getAdminView(storeName, decl.name, submitted ?? {}),
    enabled: submitted !== null || (decl.params ?? []).every((p) => !p.required),
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>{decl.title}</CardTitle>
        {decl.description && <p className="text-sm text-muted-foreground">{decl.description}</p>}
      </CardHeader>
      <CardContent>
        {(decl.params ?? []).length > 0 && (
          <div className="flex gap-2 items-end mb-3">
            {(decl.params ?? []).map((p) => (
              <label key={p.name} className="flex flex-col text-sm">
                <span className="text-muted-foreground">{p.name}{p.required ? ' *' : ''}</span>
                <input
                  className="border rounded px-2 py-1"
                  value={params[p.name] ?? ''}
                  onChange={(e) => setParams({ ...params, [p.name]: e.target.value })}
                />
              </label>
            ))}
            <Button size="sm" onClick={() => setSubmitted(params)}>Run</Button>
          </div>
        )}
        {isLoading && <p className="text-muted-foreground">Loading…</p>}
        {error && <p className="text-red-700">Error: {String(error)}</p>}
        {data && data.render_hint === 'table' && (
          <table className="w-full text-sm border">
            <thead>
              <tr className="border-b bg-muted">
                {data.schema.columns.map((c) => (
                  <th key={c.name} className="text-left px-3 py-2 font-medium">{c.name}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {(data.data?.rows ?? []).map((row: any, i: number) => (
                <tr key={i} className="border-b hover:bg-muted/30">
                  {data.schema.columns.map((c) => (
                    <td key={c.name} className="px-3 py-2 font-mono text-xs">
                      {typeof row[c.name] === 'object'
                        ? JSON.stringify(row[c.name])
                        : String(row[c.name] ?? '')}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {data && data.render_hint !== 'table' && (
          <pre className="p-2 bg-muted rounded text-xs whitespace-pre-wrap">
            {JSON.stringify(data.data, null, 2)}
          </pre>
        )}
      </CardContent>
    </Card>
  );
}
