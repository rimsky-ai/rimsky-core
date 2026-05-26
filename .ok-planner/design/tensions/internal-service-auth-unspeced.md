---
tension: internal-service-auth-unspeced
category: unspecified
status: open
affects:
  - supervisor
  - control-api
  - host-agent-proxy
---

# No mechanism for rimsky-process-to-rimsky-process authentication

## What is muddy

Rimsky has no mechanism for one rimsky process to authenticate to another. Supervisor → control-api coordination is DB-only today. The host-agent-proxy → control-api lifecycle subscription introduces a service-to-service call path (proxy subscribing to lifecycle events, proxy POSTing publisher messages to `/instances/{id}/messages`) that relies on deployment-level network isolation rather than explicit auth.

## Why it matters

Production deployments may want mTLS or service-tokens between rimsky processes. Today's posture is implicit.

## Resolution candidates (do NOT pick)

- Internal-service api-key kind with a system-permission grant.
- mTLS via per-process certificates.
- A service-mesh handoff.

## Evidence

- This spec: `.ok-planner/specs/2026-05-24-host-agent-and-proxy-design.md` §"Multi-process behavior" and §"Cache freshness".
