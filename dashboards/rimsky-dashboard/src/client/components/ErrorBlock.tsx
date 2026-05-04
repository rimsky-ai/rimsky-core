import type { TraceEvent } from '../types';

export function ErrorBlock({ event }: { event: TraceEvent }) {
  const error = event.attributes?.error ?? event.message;
  const stack = event.attributes?.stack as string | undefined;
  return (
    <div className="border border-red-200 bg-red-50 rounded-md p-3 my-2">
      <div className="font-medium text-red-900">{String(error)}</div>
      {stack && (
        <pre className="mt-2 text-xs text-red-800 whitespace-pre-wrap">{stack}</pre>
      )}
      <div className="text-xs text-red-700 mt-2">{new Date(event.timestamp).toLocaleString()}</div>
    </div>
  );
}
