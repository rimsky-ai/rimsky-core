import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { streamEvents } from '../lib/sse';
import type { TraceEvent } from '../types';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card';
import { StepTree } from './StepTree';
import { TraceEventView } from './TraceEvent';

export function TraceView({ executor, dispatchId }: { executor: string; dispatchId: string }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['trace', executor, dispatchId],
    queryFn: () => api.getTrace(executor, dispatchId),
    refetchInterval: false,
  });
  const [streamed, setStreamed] = useState<TraceEvent[]>([]);
  const [streamComplete, setStreamComplete] = useState(false);

  useEffect(() => {
    if (data?.complete) return; // already terminal, don't open SSE
    const url = `/api/exec/${executor}/trace/${dispatchId}/stream`;
    const unsub = streamEvents<TraceEvent>(
      url,
      (ev) => setStreamed((prev) => [...prev, ev]),
      () => setStreamComplete(true),
    );
    return () => unsub();
  }, [executor, dispatchId, data?.complete]);

  if (isLoading) return <p className="text-muted-foreground">Loading trace…</p>;
  if (error) return <p className="text-red-700">Error loading trace: {String(error)}</p>;
  if (!data) return null;

  // Merge by event_id (snapshot may overlap with replay).
  const byId = new Map<string, TraceEvent>();
  for (const ev of data.events ?? []) byId.set(ev.event_id, ev);
  for (const ev of streamed) byId.set(ev.event_id, ev);
  const events = Array.from(byId.values()).sort((a, b) =>
    new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime(),
  );

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          Trace
          {data.evicted && <span className="ml-2 text-xs text-muted-foreground">(evicted)</span>}
          {(data.complete || streamComplete) && <span className="ml-2 text-xs text-muted-foreground">(complete)</span>}
        </CardTitle>
      </CardHeader>
      <CardContent>
        {data.evicted ? (
          <p className="text-muted-foreground">
            Trace evicted from this executor. Snapshot empty.
          </p>
        ) : (
          <>
            <h4 className="text-sm font-semibold mt-2 mb-1">Steps</h4>
            <StepTree events={events} />
            <h4 className="text-sm font-semibold mt-4 mb-1">Log</h4>
            <div>
              {events.map((ev) => (
                <TraceEventView key={ev.event_id} event={ev} />
              ))}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}
