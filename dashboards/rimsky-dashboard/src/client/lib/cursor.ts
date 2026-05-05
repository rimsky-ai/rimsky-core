// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useState, useCallback, useRef } from 'react';

// Tiny cursor-pagination state hook. Tracks the current page's cursor
// plus a stack of previous cursors so "Prev" works without a server
// round-trip. The history stack lives in a ref so popPrev can compute
// the new history array and the new cursor independently — calling a
// state setter from inside another setter's updater function is dodgy
// and double-fires under React StrictMode.
//
// `canGoBack` reflects whether the in-memory history stack has
// anything on it; callers should disable a Prev button when false
// rather than checking `cursor === ''` (which incorrectly disables
// Prev when the caller seeded an `initial` cursor and hasn't paged).
export function useCursor(initial: string = ''): {
  cursor: string;
  canGoBack: boolean;
  pushNext: (next: string) => void;
  popPrev: () => void;
  reset: () => void;
} {
  const [cursor, setCursor] = useState(initial);
  const [historyDepth, setHistoryDepth] = useState(0);
  const historyRef = useRef<string[]>([]);

  const pushNext = useCallback(
    (next: string) => {
      historyRef.current = [...historyRef.current, cursor];
      setHistoryDepth(historyRef.current.length);
      setCursor(next);
    },
    [cursor],
  );

  const popPrev = useCallback(() => {
    const h = historyRef.current;
    if (h.length === 0) return;
    const prev = h[h.length - 1] ?? '';
    historyRef.current = h.slice(0, -1);
    setHistoryDepth(historyRef.current.length);
    setCursor(prev);
  }, []);

  const reset = useCallback(() => {
    historyRef.current = [];
    setHistoryDepth(0);
    setCursor(initial);
  }, [initial]);

  return { cursor, canGoBack: historyDepth > 0, pushNext, popPrev, reset };
}
