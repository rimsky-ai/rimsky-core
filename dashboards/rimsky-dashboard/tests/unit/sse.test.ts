// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { streamEvents } from '../../src/client/lib/sse';

class MockEventSource {
  static instances: MockEventSource[] = [];
  url: string;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }
  close() {
    this.closed = true;
  }
  emit(data: any) {
    if (this.onmessage && !this.closed) {
      this.onmessage(new MessageEvent('message', { data: JSON.stringify(data) }));
    }
  }
}

describe('streamEvents', () => {
  beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal('EventSource', MockEventSource);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('forwards events to onEvent', () => {
    const events: any[] = [];
    streamEvents<any>('/api/exec/x/trace/y/stream', (e) => events.push(e), () => {});
    const es = MockEventSource.instances[0];
    es.emit({ event_id: '1', category: 'log', timestamp: '2026-01-01' });
    es.emit({ event_id: '2', category: 'log', timestamp: '2026-01-01' });
    expect(events.length).toBe(2);
    expect(events[0].event_id).toBe('1');
  });

  it('calls onComplete on trace_complete and closes the stream', () => {
    const events: any[] = [];
    let completed = false;
    streamEvents<any>('/api/exec/x/trace/y/stream', (e) => events.push(e), () => { completed = true; });
    const es = MockEventSource.instances[0];
    es.emit({ event_id: '1', category: 'trace_complete', timestamp: '2026-01-01' });
    expect(completed).toBe(true);
    expect(es.closed).toBe(true);
  });

  it('calls onComplete on claim_terminal too', () => {
    let completed = false;
    streamEvents<any>('/api/store/x/claims/y/stream', () => {}, () => { completed = true; });
    const es = MockEventSource.instances[0];
    es.emit({ event_id: '1', category: 'claim_terminal', timestamp: '2026-01-01' });
    expect(completed).toBe(true);
  });

  it('reconnects after onerror by spinning up a new EventSource', async () => {
    vi.useFakeTimers();
    streamEvents<any>('/api/exec/x/trace/y/stream', () => {}, () => {});
    const first = MockEventSource.instances[0];
    expect(MockEventSource.instances.length).toBe(1);
    // Trigger an error: the wrapper closes and schedules a reconnect.
    first.onerror?.();
    expect(first.closed).toBe(true);
    vi.advanceTimersByTime(1500);
    expect(MockEventSource.instances.length).toBe(2);
    vi.useRealTimers();
  });
});
