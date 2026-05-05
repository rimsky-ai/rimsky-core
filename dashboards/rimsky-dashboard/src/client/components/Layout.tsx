// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { type ReactNode } from 'react';
import { Nav } from './Nav';

export function Layout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen">
      <Nav />
      <main className="flex-1 p-6 overflow-x-auto">{children}</main>
    </div>
  );
}
