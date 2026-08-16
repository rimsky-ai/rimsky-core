---
document: operating
target: docs/operating.md
---
# Operating rimsky

## Purpose
An operator standing up or running a rimsky deployment opens it to decide the deployment posture, wire the processes to each other, and keep the deployment observable. They close it with a configured stack whose peer auth, persistence, ports, deadlines, and diagnostics they can reason about.

## Covers
- the deployment postures the public images support
- the public config keys that span more than one process
- the public environment variables that govern deployment posture
- the public ports, and the pairs that collide by default
- the diagnostic and metrics HTTP routes
- the public permission actions and bundled roles, as an operator mints keys from them
