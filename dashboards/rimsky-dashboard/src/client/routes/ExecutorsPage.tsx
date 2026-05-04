import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api } from '../api';
import { Card, CardContent } from '../components/ui/card';
import { StateBadge } from '../components/StateBadge';

export default function ExecutorsPage() {
  const { data, isLoading, error } = useQuery({ queryKey: ['executors-list'], queryFn: api.listExecutors });
  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Executors</h2>
      <Card>
        <CardContent className="p-0">
          {isLoading && <p className="p-4 text-muted-foreground">Loading…</p>}
          {error && <p className="p-4 text-red-700">Error: {String(error)}</p>}
          {data && (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b bg-muted">
                  <th className="text-left px-3 py-2">name</th>
                  <th className="text-left px-3 py-2">endpoint</th>
                  <th className="text-left px-3 py-2">reachability</th>
                  <th className="text-left px-3 py-2">capabilities</th>
                </tr>
              </thead>
              <tbody>
                {(data.executors ?? []).length === 0 && (
                  <tr><td colSpan={4} className="px-3 py-6 text-center text-muted-foreground">No executors declared.</td></tr>
                )}
                {(data.executors ?? []).map((e) => (
                  <tr key={e.name} className="border-b hover:bg-muted/30">
                    <td className="px-3 py-2"><Link to={`/executors/${e.name}`} className="hover:underline font-mono">{e.name}</Link></td>
                    <td className="px-3 py-2 font-mono text-xs">{e.endpoint}</td>
                    <td className="px-3 py-2"><StateBadge value={e.reachability_status} /></td>
                    <td className="px-3 py-2 text-xs text-muted-foreground">
                      {e.observability_capabilities ? (
                        <>
                          {e.observability_capabilities.supports_trace_get && <span className="mr-2">trace_get</span>}
                          {e.observability_capabilities.supports_trace_stream && <span className="mr-2">trace_stream</span>}
                        </>
                      ) : '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
