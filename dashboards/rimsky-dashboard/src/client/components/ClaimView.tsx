import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card';
import { StateBadge } from './StateBadge';
import { TraceEventView } from './TraceEvent';

export function ClaimView({ storeName, claimId }: { storeName: string; claimId: string }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['claim', storeName, claimId],
    queryFn: () => api.getClaim(storeName, claimId),
  });
  if (isLoading) return <p className="text-muted-foreground">Loading claim…</p>;
  if (error) return <p className="text-red-700">Error: {String(error)}</p>;
  if (!data) return null;
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Claim {claimId}
          <StateBadge value={data.state} />
        </CardTitle>
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-2 gap-2 text-sm mb-3">
          <dt className="text-muted-foreground">opened_at</dt>
          <dd>{data.opened_at ? new Date(data.opened_at).toLocaleString() : '—'}</dd>
          <dt className="text-muted-foreground">closed_at</dt>
          <dd>{data.closed_at ? new Date(data.closed_at).toLocaleString() : '—'}</dd>
        </dl>
        {data.address !== undefined && (
          <details className="mb-2">
            <summary className="cursor-pointer text-sm text-muted-foreground">address</summary>
            <pre className="mt-1 p-2 bg-muted rounded text-xs whitespace-pre-wrap">
              {JSON.stringify(data.address, null, 2)}
            </pre>
          </details>
        )}
        {data.payload !== undefined && (
          <details className="mb-2">
            <summary className="cursor-pointer text-sm text-muted-foreground">payload</summary>
            <pre className="mt-1 p-2 bg-muted rounded text-xs whitespace-pre-wrap">
              {JSON.stringify(data.payload, null, 2)}
            </pre>
          </details>
        )}
        {data.region !== undefined && (
          <details className="mb-2">
            <summary className="cursor-pointer text-sm text-muted-foreground">region</summary>
            <pre className="mt-1 p-2 bg-muted rounded text-xs whitespace-pre-wrap">
              {JSON.stringify(data.region, null, 2)}
            </pre>
          </details>
        )}
        <h4 className="text-sm font-semibold mt-3 mb-1">History</h4>
        {(data.history ?? []).map((ev) => (
          <TraceEventView key={ev.event_id} event={ev} />
        ))}
      </CardContent>
    </Card>
  );
}
