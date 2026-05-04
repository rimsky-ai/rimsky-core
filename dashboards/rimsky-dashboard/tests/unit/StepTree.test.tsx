import { describe, it, expect } from 'vitest';
import { buildStepTree } from '../../src/client/components/StepTree';
import type { TraceEvent } from '../../src/client/types';

describe('buildStepTree', () => {
  it('groups nested steps via parent_event_id', () => {
    const events: TraceEvent[] = [
      { event_id: 'a', timestamp: 't', severity: 'INFO', category: 'step_started', attributes: { step_id: 'A' } },
      {
        event_id: 'b',
        timestamp: 't',
        severity: 'INFO',
        category: 'step_started',
        parent_event_id: 'a',
        attributes: { step_id: 'B' },
      },
      { event_id: 'c', timestamp: 't', severity: 'INFO', category: 'step_completed', attributes: { step_id: 'B' } },
      { event_id: 'd', timestamp: 't', severity: 'INFO', category: 'step_completed', attributes: { step_id: 'A' } },
    ];
    const roots = buildStepTree(events);
    expect(roots.length).toBe(1);
    expect(roots[0].id).toBe('A');
    expect(roots[0].children.length).toBe(1);
    expect(roots[0].children[0].id).toBe('B');
    expect(roots[0].end?.category).toBe('step_completed');
  });
});
