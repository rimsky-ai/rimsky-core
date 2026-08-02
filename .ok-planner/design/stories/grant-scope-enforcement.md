---
story: grant-scope-enforcement
---

# Least-privilege delegation across lifecycle

## Story

As an operator delegating control-plane access to a per-tenant agent, I can scope an api-key's grant to a specific resource (e.g., a template-tag), with the permission matcher refusing requests against any other resource of the same action across the resource's full lifecycle, so that least-privilege delegation is enforced rather than just believed.
