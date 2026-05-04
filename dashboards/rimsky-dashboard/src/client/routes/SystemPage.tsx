import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { StateBadge } from '../components/StateBadge';

export default function SystemPage() {
  const health = useQuery({ queryKey: ['system-health'], queryFn: api.systemHealth });
  const summary = useQuery({ queryKey: ['system-summary'], queryFn: api.systemSummary });
  const stores = useQuery({ queryKey: ['stores-list'], queryFn: api.listStores });
  const executors = useQuery({ queryKey: ['executors-list'], queryFn: api.listExecutors });

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
