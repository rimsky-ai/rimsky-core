import { Hono } from 'hono';

import { discoveryCacheStatus, invalidateDiscoveryCache } from './discovery.js';

// admin exposes server-local admin endpoints. None of these proxy to
// the control-api — they are dashboard-process only.
//
//	GET  /api/admin/discovery   → cache age + counts
//	POST /api/admin/refresh-discovery → invalidate cache; next lookup re-fetches
//
// Per issues 25 + 31: operators need a way to force-refresh the
// dashboard's discovery cache without restarting the process, and the
// UI needs cache-age signal to surface "discovery is stale" banners.
export const admin = new Hono();

admin.get('/api/admin/discovery', (c) => {
  return c.json(discoveryCacheStatus());
});

admin.post('/api/admin/refresh-discovery', (c) => {
  invalidateDiscoveryCache();
  return c.json({ refreshed: true });
});
