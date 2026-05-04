import type { TraceEvent } from '../types';
import { Badge } from './ui/badge';

export function ToolCallInspector({ event }: { event: TraceEvent }) {
  const toolName = (event.attributes?.tool_name as string) ?? 'unknown';
  const args = event.attributes?.arguments;
  const result = event.attributes?.result;
  const duration = event.attributes?.duration_ms as number | undefined;
  return (
    <div className="border rounded-md p-3 my-2 bg-background">
      <div className="flex items-center gap-2">
        <span className="font-mono text-sm">tool: {toolName}</span>
        {duration !== undefined && <Badge variant="muted">{duration}ms</Badge>}
        <span className="ml-auto text-xs text-muted-foreground">
          {new Date(event.timestamp).toLocaleString()}
        </span>
      </div>
      <details className="mt-2">
        <summary className="cursor-pointer text-xs text-muted-foreground">arguments</summary>
        <pre className="mt-1 p-2 bg-muted rounded text-xs whitespace-pre-wrap">
          {JSON.stringify(args, null, 2)}
        </pre>
      </details>
      <details className="mt-1">
        <summary className="cursor-pointer text-xs text-muted-foreground">result</summary>
        <pre className="mt-1 p-2 bg-muted rounded text-xs whitespace-pre-wrap">
          {JSON.stringify(result, null, 2)}
        </pre>
      </details>
    </div>
  );
}
