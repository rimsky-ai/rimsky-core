import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api } from '../api';
import { Card, CardContent } from '../components/ui/card';
import { StateBadge } from '../components/StateBadge';

export default function StoresPage() {
  const { data, isLoading, error } = useQuery({ queryKey: ['stores-list'], queryFn: api.listStores });
  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Stores</h2>
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
                {(data.stores ?? []).length === 0 && (
                  <tr><td colSpan={4} className="px-3 py-6 text-center text-muted-foreground">No stores declared.</td></tr>
                )}
                {(data.stores ?? []).map((s) => (
                  <tr key={s.name} className="border-b hover:bg-muted/30">
                    <td className="px-3 py-2"><Link to={`/stores/${s.name}`} className="hover:underline font-mono">{s.name}</Link></td>
                    <td className="px-3 py-2 font-mono text-xs">{s.endpoint}</td>
                    <td className="px-3 py-2"><StateBadge value={s.reachability_status} /></td>
                    <td className="px-3 py-2 text-xs text-muted-foreground">
                      {s.observability_capabilities ? (
                        <>
                          {s.observability_capabilities.supports_claim_get && <span className="mr-2">claim_get</span>}
                          {s.observability_capabilities.supports_list_claims && <span className="mr-2">list_claims</span>}
                          {(s.observability_capabilities.admin_views ?? []).length > 0 && (
                            <span>{s.observability_capabilities.admin_views!.length} admin views</span>
                          )}
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
