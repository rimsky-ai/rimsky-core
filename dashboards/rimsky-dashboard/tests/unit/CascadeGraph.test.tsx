// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CascadeGraph } from '../../src/client/components/CascadeGraph';
import type { CascadeNode } from '../../src/client/types';

describe('CascadeGraph', () => {
  it('renders an SVG with one rect per node', () => {
    const nodes: CascadeNode[] = [
      { node_type: 'a', node_id: '1', state: 'fresh', retry_counter: 0, edges_in: [], edges_out: ['b'] },
      { node_type: 'b', node_id: '2', state: 'running', retry_counter: 0, edges_in: ['a'], edges_out: [] },
    ];
    const { container } = render(
      <MemoryRouter>
        <CascadeGraph nodes={nodes} instanceId="i1" />
      </MemoryRouter>,
    );
    expect(container.querySelector('svg')).not.toBeNull();
    expect(container.querySelectorAll('rect').length).toBe(2);
    expect(container.querySelectorAll('line').length).toBe(1);
  });
  it('renders the empty placeholder when there are no nodes', () => {
    const { getByText } = render(
      <MemoryRouter>
        <CascadeGraph nodes={[]} instanceId="i1" />
      </MemoryRouter>,
    );
    expect(getByText(/No nodes/i)).toBeInTheDocument();
  });
});
