// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// EventSource wrapper with automatic reconnection + exponential
// backoff. Returns an unsubscribe function. The caller passes
// onComplete; the wrapper invokes it when an upstream "trace_complete"
// or "claim_terminal" event arrives, then closes the connection.
//
// Reconnect schedule: 1s, 2s, 4s, 8s, 16s, 30s (capped). After
// MAX_ATTEMPTS consecutive failures the wrapper gives up and reports
// the lost-stream state via onError so the consumer hook can render a
// "stream lost" badge / retry button.

export type SseUnsubscribe = () => void;

const INITIAL_DELAY_MS = 1_000;
const MAX_DELAY_MS = 30_000;
const MAX_ATTEMPTS = 10;

export class SseStreamLostError extends Error {
  readonly attempts: number;
  constructor(attempts: number) {
    super(`SSE stream lost after ${attempts} reconnect attempts`);
    this.name = 'SseStreamLostError';
    this.attempts = attempts;
  }
}

export function streamEvents<T>(
  url: string,
  onEvent: (event: T) => void,
  onComplete: () => void,
  onError?: (err: Error) => void,
): SseUnsubscribe {
  let closed = false;
  let attempts = 0;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let es: EventSource | null = null;

  const wireUp = (source: EventSource) => {
    source.onopen = () => {
      // Successful connection resets the backoff window so a long-
      // running stream that briefly drops doesn't immediately give up.
      attempts = 0;
    };
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
      attempts++;
      if (attempts >= MAX_ATTEMPTS) {
        closed = true;
        onError?.(new SseStreamLostError(attempts));
        return;
      }
      const delay = Math.min(INITIAL_DELAY_MS * 2 ** (attempts - 1), MAX_DELAY_MS);
      timer = setTimeout(() => {
        timer = null;
        if (!closed) {
          es = new EventSource(url);
          wireUp(es);
        }
      }, delay);
    };
  };

  es = new EventSource(url);
  wireUp(es);

  return () => {
    closed = true;
    if (timer !== null) clearTimeout(timer);
    es?.close();
  };
}
