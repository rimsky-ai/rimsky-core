import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { Button } from '../components/ui/button';
import { StateBadge } from '../components/StateBadge';

type DiscoveryStatus = {
  last_refresh_ms: number | null;
  age_ms: number | null;
  ttl_ms: number;
  executor_count: number;
  store_count: number;
};

export default function SystemPage() {
  const health = useQuery({ queryKey: ['system-health'], queryFn: api.systemHealth });
  const summary = useQuery({ queryKey: ['system-summary'], queryFn: api.systemSummary });
  const stores = useQuery({ queryKey: ['stores-list'], queryFn: api.listStores });
  const executors = useQuery({ queryKey: ['executors-list'], queryFn: api.listExecutors });
  const qc = useQueryClient();
  const discovery = useQuery({
    queryKey: ['discovery-status'],
    queryFn: async (): Promise<DiscoveryStatus> => {
      const r = await fetch('/api/admin/discovery');
      if (!r.ok) throw new Error(`discovery: ${r.status}`);
      return r.json() as Promise<DiscoveryStatus>;
    },
    refetchInterval: 5_000,
  });
  const refreshDiscovery = async () => {
    await fetch('/api/admin/refresh-discovery', { method: 'POST' });
    await qc.invalidateQueries();
  };

  return (
    <div className="grid gap-6 grid-cols-1 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>Health</CardTitle>
        </CardHeader>
        <CardContent>
          {health.isLoading && <p className="text-muted-foreground">Loading…</p>}
          {health.error && <p className="text-red-700">Error: {String(health.error)}</p>}
          {health.data && (
            <dl className="grid grid-cols-2 gap-2 text-sm">
              <dt className="text-muted-foreground">control-api</dt>
              <dd><StateBadge value={health.data.control_api_status} /></dd>
              <dt className="text-muted-foreground">supervisors</dt>
              <dd>{(health.data.supervisors ?? []).length}</dd>
              <dt className="text-muted-foreground">stores</dt>
              <dd>{(health.data.stores ?? []).length}</dd>
              <dt className="text-muted-foreground">executors</dt>
              <dd>{(health.data.executors ?? []).length}</dd>
            </dl>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Summary</CardTitle>
        </CardHeader>
        <CardContent>
          {summary.isLoading && <p className="text-muted-foreground">Loading…</p>}
          {summary.data && (
            <div className="grid grid-cols-2 gap-2 text-sm">
              <div>active instances</div>
              <div className="font-medium">{summary.data.instances_active}</div>
              <div>terminated instances</div>
              <div className="font-medium">{summary.data.instances_terminated}</div>
              <div>dispatches claimed</div>
              <div className="font-medium">{summary.data.dispatches_claimed ?? 0}</div>
              <div>dispatches pending</div>
              <div className="font-medium">{summary.data.dispatches_pending ?? 0}</div>
              <div className="col-span-2 mt-3 font-medium">node states</div>
              {Object.entries(summary.data.node_counts ?? {}).map(([k, v]) => (
                <div key={k} className="contents">
                  <div className="pl-2"><StateBadge value={k} /></div>
                  <div>{v}</div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Discovery cache</CardTitle>
        </CardHeader>
        <CardContent>
          {discovery.isLoading && <p className="text-muted-foreground">Loading…</p>}
          {discovery.data && (
            <div className="grid grid-cols-2 gap-2 text-sm">
              <div className="text-muted-foreground">last refresh</div>
              <div>
                {discovery.data.last_refresh_ms
                  ? new Date(discovery.data.last_refresh_ms).toLocaleTimeString()
                  : 'never'}
              </div>
              <div className="text-muted-foreground">age</div>
              <div>
                {discovery.data.age_ms !== null
                  ? `${Math.round(discovery.data.age_ms / 1000)}s`
                  : '—'}
                {discovery.data.age_ms !== null &&
                  discovery.data.age_ms > discovery.data.ttl_ms && (
                    <span className="ml-2 rounded bg-amber-100 px-1.5 text-xs text-amber-900">
                      stale
                    </span>
                  )}
              </div>
              <div className="text-muted-foreground">ttl</div>
              <div>{Math.round(discovery.data.ttl_ms / 1000)}s</div>
              <div className="text-muted-foreground">peers</div>
              <div>
                {discovery.data.executor_count} executors,{' '}
                {discovery.data.store_count} stores
              </div>
              <div className="col-span-2 mt-2">
                <Button onClick={refreshDiscovery} variant="outline" size="sm">
                  Refresh discovery
                </Button>
              </div>
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Stores</CardTitle>
        </CardHeader>
        <CardContent>
          {(stores.data?.stores ?? []).length === 0 && <p className="text-muted-foreground text-sm">No stores declared.</p>}
          {(stores.data?.stores ?? []).map((s) => (
            <div key={s.name} className="flex items-center gap-2 py-1">
              <Link to={`/stores/${s.name}`} className="hover:underline font-mono text-sm">{s.name}</Link>
              <StateBadge value={s.reachability_status} />
              {s.observability_capabilities && (
                <span className="text-xs text-muted-foreground">
                  {(s.observability_capabilities.admin_views ?? []).length} admin views
                </span>
              )}
            </div>
          ))}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Executors</CardTitle>
        </CardHeader>
        <CardContent>
          {(executors.data?.executors ?? []).length === 0 && <p className="text-muted-foreground text-sm">No executors declared.</p>}
          {(executors.data?.executors ?? []).map((e) => (
            <div key={e.name} className="flex items-center gap-2 py-1">
              <Link to={`/executors/${e.name}`} className="hover:underline font-mono text-sm">{e.name}</Link>
              <StateBadge value={e.reachability_status} />
              {e.observability_capabilities?.supports_trace_get && (
                <span className="text-xs text-muted-foreground">trace</span>
              )}
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
