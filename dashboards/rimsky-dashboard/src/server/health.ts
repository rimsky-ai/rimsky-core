import { Hono } from 'hono';
import { config } from './config.js';

export const health = new Hono();

health.get('/healthz', async (c) => {
  try {
    const r = await fetch(`${config.controlApiUrl}/health`);
    if (r.ok) return c.text('ok', 200);
    return c.text('control-api unreachable', 503);
  } catch {
    return c.text('control-api unreachable', 503);
  }
});
