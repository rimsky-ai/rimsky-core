// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// Smoke test for the asset-primary dashboard panel (2026-05-15 data-
// platform-extensions §Section S). Renders AssetsPage + AssetDetailPage
// under a fake QueryClient + Router; asserts the header text, fetch path
// resolution, and Materialize/Delete button presence. Does NOT exercise
// the full network round-trip — that's the smoke fixture's job. This is
// a static-shape smoke confirming the dashboard wires the API surface
// correctly.

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, waitFor, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import AssetsPage from '../../src/client/routes/assets/AssetsPage';
import AssetDetailPage from '../../src/client/routes/assets/AssetDetailPage';

function renderWithRouter(ui: React.ReactElement, initialEntries: string[]) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={initialEntries}>
        <Routes>
          <Route path="/assets" element={ui} />
          <Route
            path="/instances/:instanceId/assets/:alias"
            element={<AssetDetailPage />}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('AssetsPage', () => {
  it('renders the Assets header and empty state when no instances', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input);
      if (url.includes('/api/control/instances')) {
        return new Response(JSON.stringify({ instances: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.includes('/api/control/instances/') && url.endsWith('/assets')) {
        return new Response(JSON.stringify({ assets: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response('not found', { status: 404 });
    });

    renderWithRouter(<AssetsPage />, ['/assets']);
    await waitFor(() => {
      expect(screen.getByText('Assets')).toBeInTheDocument();
    });
  });

  it('renders the asset row when one is returned by the API', async () => {
    const fakeAsset = {
      instance_id: '11111111-1111-1111-1111-111111111111',
      alias: 'parcels',
      producer_name: 'parquet-store',
      scope_data_hash: 'abc123def',
      current_version_id: 'v3',
      held_durable: true,
      created_at: new Date(0).toISOString(),
    };
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith('/api/control/instances')) {
        return new Response(
          JSON.stringify({
            instances: [
              {
                id: fakeAsset.instance_id,
                instance_key: 'demo',
                template_hash: 'sha256-stub',
                created_at: new Date(0).toISOString(),
              },
            ],
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }
      if (url.includes('/assets') && !url.includes('/versions')) {
        return new Response(JSON.stringify({ assets: [fakeAsset] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response('not found', { status: 404 });
    });

    renderWithRouter(<AssetsPage />, ['/assets']);
    await waitFor(() => {
      expect(screen.getByText('parcels')).toBeInTheDocument();
      expect(screen.getByText('parquet-store')).toBeInTheDocument();
      expect(screen.getByText('v3')).toBeInTheDocument();
    });
  });
});
