import type { TraceEvent } from '../types';
import { Badge } from './ui/badge';

export function LogLine({ event }: { event: TraceEvent }) {
  const variant: 'info' | 'warning' | 'error' | 'muted' =
    event.severity === 'ERROR' ? 'error' : event.severity === 'WARN' ? 'warning' : event.severity === 'DEBUG' ? 'muted' : 'info';
  return (
    <div className="font-mono text-xs flex items-start gap-2 py-1 border-b border-muted/40">
      <span className="text-muted-foreground tabular-nums">
        {new Date(event.timestamp).toLocaleTimeString()}
      </span>
      <Badge variant={variant}>{event.severity}</Badge>
      <span className="text-muted-foreground">{event.category}</span>
      <span className="flex-1">{event.message}</span>
      {event.attributes && Object.keys(event.attributes).length > 0 && (
        <details>
          <summary className="cursor-pointer text-muted-foreground">attrs</summary>
          <pre className="mt-1 ml-2 p-2 bg-muted rounded text-xs whitespace-pre-wrap">
            {JSON.stringify(event.attributes, null, 2)}
          </pre>
        </details>
      )}
    </div>
  );
}
