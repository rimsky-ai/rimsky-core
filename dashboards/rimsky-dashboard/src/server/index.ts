import { Hono } from 'hono';
import { serve } from '@hono/node-server';
import { serveStatic } from '@hono/node-server/serve-static';

import { proxy } from './proxy.js';
import { health } from './health.js';
import { admin } from './admin.js';
import { config } from './config.js';

const app = new Hono();

// Strict CSP header on every response (spec §5.6). Forbids inline
// script; allows operator-declared CustomUI panels via frame-src *.
// connect-src is locked to 'self' since the proxy collapses CORS to
// a single origin.
const CSP =
  "default-src 'self'; " +
  "script-src 'self'; " +
  "style-src 'self' 'unsafe-inline'; " +
  "img-src 'self' data: blob:; " +
  "connect-src 'self'; " +
  "frame-src 'self' *; " +
  "font-src 'self' data:; " +
  "base-uri 'self'; " +
  "form-action 'self';";
app.use('*', async (c, next) => {
  await next();
  c.header('Content-Security-Policy', CSP);
  c.header('X-Content-Type-Options', 'nosniff');
});

app.route('/', health);
app.route('/', admin);
app.route('/', proxy);
app.use('/*', serveStatic({ root: './dist/client' }));
app.use('/*', serveStatic({ root: './dist/client', path: 'index.html' }));

const server = serve({ fetch: app.fetch, port: config.port }, (info) => {
  console.log(`rimsky-dashboard listening on :${info.port}`);
});

const shutdown = (signal: string) => {
  console.log(`rimsky-dashboard: received ${signal}, shutting down`);
  server.close((err) => {
    if (err) {
      console.error('rimsky-dashboard: close error:', err);
      process.exit(1);
    }
    process.exit(0);
  });
  // Hard timeout: never hang past 10s on a stuck connection.
  setTimeout(() => process.exit(0), 10_000).unref();
};
process.on('SIGTERM', () => shutdown('SIGTERM'));
process.on('SIGINT', () => shutdown('SIGINT'));
process.on('uncaughtException', (err) => {
  console.error('rimsky-dashboard: uncaughtException:', err);
  process.exit(1);
});
process.on('unhandledRejection', (reason) => {
  console.error('rimsky-dashboard: unhandledRejection:', reason);
});
