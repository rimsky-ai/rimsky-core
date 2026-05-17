// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { NavLink } from 'react-router-dom';
import { cn } from '../lib/utils';

const sections: { title: string; links: { path: string; label: string }[] }[] = [
  {
    title: 'System',
    links: [{ path: '/', label: 'Overview' }],
  },
  {
    title: 'Workflow',
    links: [
      { path: '/templates', label: 'Templates' },
      { path: '/instances', label: 'Instances' },
      { path: '/assets', label: 'Assets' },
      { path: '/events', label: 'Events' },
    ],
  },
  {
    title: 'Runtime',
    links: [
      { path: '/frames', label: 'Frames' },
      { path: '/node-runs', label: 'Node-runs' },
      { path: '/lock-holders', label: 'Lock holders' },
      { path: '/parked-nodes', label: 'Parked nodes' },
    ],
  },
  {
    title: 'Topology',
    links: [
      { path: '/stores', label: 'Stores' },
      { path: '/executors', label: 'Executors' },
    ],
  },
];

export function Nav() {
  return (
    <nav className="w-56 border-r bg-muted/40 p-4 flex flex-col gap-4 text-sm">
      <div className="font-semibold text-base mb-2">Rimsky</div>
      {sections.map((s) => (
        <div key={s.title}>
          <div className="text-xs font-semibold uppercase text-muted-foreground tracking-wide mb-1">
            {s.title}
          </div>
          <div className="flex flex-col">
            {s.links.map((l) => (
              <NavLink
                key={l.path}
                to={l.path}
                end={l.path === '/'}
                className={({ isActive }) =>
                  cn(
                    'px-2 py-1 rounded-md hover:bg-accent',
                    isActive ? 'bg-accent text-accent-foreground' : 'text-foreground',
                  )
                }
              >
                {l.label}
              </NavLink>
            ))}
          </div>
        </div>
      ))}
    </nav>
  );
}
