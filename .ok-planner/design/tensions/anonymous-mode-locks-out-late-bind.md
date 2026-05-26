---
tension: anonymous-mode-locks-out-late-bind
category: unclear
status: open
affects:
  - anonymous-mode
  - host-agent-proxy
  - instance
---

# Anonymous-mode users can register late-bind templates but cannot dispatch them

## What is muddy

Anonymous-mode (`Identity.KeyID == nil`) leaves `rimsky_instances.created_by_api_key_id` null. The proxy's routing requires the owner-api-key to find a connected agent, so any late-bound service dispatch fails with `host_agent_not_connected` for anonymous-mode-created instances. Anonymous-mode users can register templates and trigger workflows, but cannot use late-bound services.

## Why it matters

Anonymous-mode is the documented bootstrap path for unauthenticated rimsky deployments. Locking it out of a major feature is a real product asymmetry. For dev-machine workflows (the host-agent's primary use case) the user is already authenticated, so the v1 constraint is acceptable; but a hosted deployment where some users are anonymous and want code-gen would hit this wall.

## Resolution candidates (do NOT pick)

- Emit a synthetic admin identity (already done; but the request still carries no api-key identity).
- Allow the CLI to supply an "agent-routing-key" header that the proxy uses instead of the instance's creating-api-key identity (see `concept:host-agent-proxy`, `concept:instance`).
- Require all late-bind-using deployments to disable anonymous-mode.

## Evidence

- This spec: `.ok-planner/specs/2026-05-24-host-agent-and-proxy-design.md` §"Per-instance service bindings — Owner identity".
