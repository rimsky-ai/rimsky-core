// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// Server config — loaded from env. Per spec §5.3 the server reads
// only RIMSKY_CONTROL_API_URL and PORT.
export const config = {
  port: Number(process.env.PORT ?? 8090),
  controlApiUrl: process.env.RIMSKY_CONTROL_API_URL ?? 'http://control-api:8080',
};
