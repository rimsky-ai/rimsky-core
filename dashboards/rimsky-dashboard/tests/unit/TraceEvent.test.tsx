// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { TraceEventView } from '../../src/client/components/TraceEvent';
import type { TraceEvent } from '../../src/client/types';

const base = (overrides: Partial<TraceEvent>): TraceEvent => ({
  event_id: 'e1',
  timestamp: '2026-05-03T00:00:00Z',
  severity: 'INFO',
  category: 'log',
  message: 'hello',
  ...overrides,
});

describe('TraceEventView', () => {
  it('renders tool_call as the inspector', () => {
    const ev = base({
      category: 'tool_call',
      attributes: { tool_name: 'read_file', arguments: { path: 'x' }, result: { ok: true } },
    });
    const { getByText } = render(<TraceEventView event={ev} />);
    expect(getByText(/tool: read_file/)).toBeInTheDocument();
  });
  it('renders error as ErrorBlock', () => {
    const ev = base({ category: 'error', severity: 'ERROR', attributes: { error: 'boom' } });
    const { getByText } = render(<TraceEventView event={ev} />);
    expect(getByText('boom')).toBeInTheDocument();
  });
  it('renders log as LogLine with the message', () => {
    const ev = base({ category: 'log', message: 'just-a-line' });
    const { getByText } = render(<TraceEventView event={ev} />);
    expect(getByText('just-a-line')).toBeInTheDocument();
  });
});
