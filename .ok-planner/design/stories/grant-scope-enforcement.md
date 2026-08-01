---
story: grant-scope-enforcement
status: as-is
---

# Least-privilege delegation across lifecycle

## Story

As an operator delegating control-plane access to a per-tenant agent, I can scope an api-key's grant to a specific resource (e.g., a template-tag), with the permission matcher refusing requests against any other resource of the same action across the resource's full lifecycle, so that least-privilege delegation is enforced rather than just believed.

Per-grant resource scope: action-specific dimension keys (e.g., a template-tag dimension) constrain a key's access to a specific resource. Scope checks fire across the resource's full lifecycle, not just at register.

Least-privilege delegation is enforced rather than just believed; an agent granted access to one tenant's tag cannot escalate to another by issuing later-lifecycle verbs.
