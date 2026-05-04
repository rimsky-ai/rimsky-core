import { Hono } from 'hono';
import { serve } from '@hono/node-server';
import { serveStatic } from '@hono/node-server/serve-static';

import { proxy } from './proxy.js';
import { health } from './health.js';
import { config } from './config.js';

const app = new Hono();
app.route('/', health);
app.route('/', proxy);
app.use('/*', serveStatic({ root: './dist/client' }));
app.use('/*', serveStatic({ root: './dist/client', path: 'index.html' }));

serve({ fetch: app.fetch, port: config.port });
console.log(`rimsky-dashboard listening on :${config.port}`);
