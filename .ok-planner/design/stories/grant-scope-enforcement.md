---
story: grant-scope-enforcement
status: as-is
---

# Least-privilege delegation across lifecycle

## Role

As an operator delegating control-plane access to a per-tenant agent, I can scope an api-key's grant to a specific resource (e.g., a template-tag), with the permission matcher refusing requests against any other resource of the same action across the resource's full lifecycle, so that least-privilege delegation is enforced rather than just believed.

## Capability

Per-grant resource scope: action-specific dimension keys (e.g., a template-tag dimension) constrain a key's access to a specific resource. Scope checks fire across the resource's full lifecycle, not just at register.

## Business value

Least-privilege delegation is enforced rather than just believed; an agent granted access to one tenant's tag cannot escalate to another by issuing later-lifecycle verbs.

## Acceptance

An operator mints an api-key whose grant scopes a write action to a single resource (e.g., binding the template-tag dimension to one specific tag name); an in-scope request succeeds; an out-of-scope request of the same action is refused, recorded as an auth-denied audit row (the denial reason is a single undifferentiated value shared with every other permission refusal — the audit row does not distinguish a scope mismatch from an action-level denial); the out-of-scope resource is not created. Scope enforcement covers the full lifecycle of the resource (register, deploy, undeploy, deregister, tag set, tag delete, instance create), not just register.

## Falsifier

An out-of-scope request succeeds, OR a same-action operation later in the lifecycle silently bypasses the scope check.

## Proof

Executable proof.
