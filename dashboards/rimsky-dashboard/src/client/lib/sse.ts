// EventSource wrapper with automatic reconnection. Returns an unsubscribe
// function. The caller passes onComplete; the wrapper invokes it when an
// upstream "trace_complete" or "claim_terminal" event arrives, then
// closes the connection.

export type SseUnsubscribe = () => void;

export function streamEvents<T>(
  url: string,
  onEvent: (event: T) => void,
  onComplete: () => void,
  onError?: (err: Error) => void,
): SseUnsubscribe {
  let es = new EventSource(url);
  let closed = false;

  const wireUp = (source: EventSource) => {
    source.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data) as T & { category?: string };
        if (data.category === 'trace_complete' || data.category === 'claim_terminal') {
          onComplete();
          source.close();
          closed = true;
          return;
        }
        onEvent(data);
      } catch (err) {
        onError?.(err as Error);
      }
    };
    source.onerror = () => {
      if (closed) return;
      source.close();
      setTimeout(() => {
        if (!closed) {
          es = new EventSource(url);
          wireUp(es);
        }
      }, 1000);
    };
  };

  wireUp(es);

  return () => {
    closed = true;
    es.close();
  };
}
