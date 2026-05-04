import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { StateBadge } from '../../src/client/components/StateBadge';

describe('StateBadge', () => {
  it('renders the value as text', () => {
    const { getByText } = render(<StateBadge value="running" />);
    expect(getByText('running')).toBeInTheDocument();
  });
  it('renders em-dash for empty value', () => {
    const { getByText } = render(<StateBadge value={null} />);
    expect(getByText('—')).toBeInTheDocument();
  });
});
