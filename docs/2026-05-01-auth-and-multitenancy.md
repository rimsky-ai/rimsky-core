# Rimsky Auth & Multi-tenancy Design

## Status

- Design notes, 2026-05-01.
- Spun out of `docs/2026-04-26-control-layer.md` (originally §3 of that doc, before it was settled). The control-layer doc now covers provisioning and registry shape only; auth and multi-tenancy live here so the control-layer story doesn't get tangled with deployment-model questions.
- One settled position (§1 per-project deployment, no auth in v1), one deferred forward path (§2 principal field + policy hook), one split topic (§3: substrate-supported multi-tenancy available under v1 via the lifecycle-events model in `docs/2026-04-26-control-layer.md` §1; rimsky-enforced multi-tenancy deferred).
- **Scope boundary.** The runtime (scheduler, supervisor) is auth-blind by design (per the auth-blind philosophy in `docs/2026-04-25-store-redesign.md` §14). Auth is a control-api concern. The runtime never sees principal or tenant identity at dispatch; it sees fully-resolved templates, instances, and store registrations.

## Context

Rimsky's runtime is auth-blind. The control-api is the surface that interacts with operators and users — registering templates, creating instances, invalidating, observing state. That surface needs an answer to "who's calling and what are they allowed to do?"

The answer depends on how rimsky is deployed. The 2026-05-01 conversation distinguished three deployment shapes and committed to a v1 stance.

## 1. Settled position: per-project deployment, no rimsky-side auth in v1

### 1.1 The three deployment shapes

| Shape | Tenancy = | What rimsky needs to know | What pays for it |
|---|---|---|---|
| Per-project / per-team | The deployment itself | Nothing | The operator's auth perimeter (mesh, IAM, ingress) |
| Shared, single-tenant ops | Ops as a single tenant | Principal identity + a policy hook | A `principal` field + an ACL/policy layer in the control-api |
| Shared, multi-tenant | Per-tenant isolation | Principal + tenant identity + scope-aware policy | All of the above + `tenant_id` on every state row + per-tenant store-service config + cross-tenant isolation invariants |

Each step adds real surface to the control-api, the schema, the audit log, and (in the multi-tenant case) the store-service contract.

### 1.2 v1 commitment: per-project only

V1 rimsky deploys as a per-project / per-team infrastructure piece. One rimsky per workload (or per tightly-coupled set of workloads). The operator brings their own auth perimeter — service mesh, IAM, ingress auth, mTLS, whatever they already use to gate access to internal services. Rimsky does not authenticate, does not authorize, does not know who's calling.

The control-api accepts whatever lands at it. A network-level perimeter (k8s NetworkPolicy, security group, mesh policy) is the deployment-time answer for "who can reach the control-api."

This is the same operational shape as Postgres, Redis, Kafka, NATS, or any other internal infra service. Rimsky is one of those.

### 1.3 Why this is the right v1 stance

- **Scope discipline.** Auth done well is a substantial spec — authentication mechanisms, principal modelling, policy hooks, audit logging, credential rotation, federation. Doing auth poorly is worse than not doing it (false sense of security, lock-in to a bad model). v1 has more pressing scope.
- **Clean isolation by deployment topology.** Per-project deployment gives real isolation: separate scheduler, supervisor, control-api, postgres state DB. No software-enforced cross-tenant invariants to verify. If a workload misbehaves (DoS the scheduler, fill postgres), it doesn't affect anyone else.
- **No false promises.** A "we have auth" deployment that's actually per-project anyway is misleading. Better to be explicit: rimsky is infra, put it behind your auth perimeter.
- **Forward-compatible.** Every state-mutating endpoint can grow a principal field later (§2) without breaking existing deployments. The v1 stance does not foreclose anything.

### 1.4 What this means for operators

- The control-api binds to an internal address. External access is gated by the operator's existing perimeter.
- Network policies / firewall rules / mesh policies are the v1 access control story.
- Any operator command (`rimsky-cli register`, `rimsky-cli instantiate`, force-fire, kill, query state) is allowed if the network connection succeeds.
- Audit logging records the time and the action; not the principal (there is none).
- Multi-tenant workloads run as separate rimsky deployments — one per tenant. The operator's IaC handles the per-tenant rimsky provisioning.

### 1.5 What changes in the control-layer doc and the codebase

- `rimsky_templates` and `rimsky_instances` do not get a `principal` column or a `tenant_id` column in v1.
- Audit log entries (when added) record action + timestamp + relevant target IDs; no principal field.
- The control-api binds to whatever address ops configure (today's behavior); no change.
- The §2 registry-shape policy knob ("accept-direct-registration: anyone / listed principals / refuse") simplifies to "always allow" in v1; the policy hook stays in the design as the integration point for §2 of this doc.

## 2. Deferred: principal field and policy hook (the shared-single-tenant case)

### 2.1 What this would add

If a future deployment wants one rimsky for a whole org (shared-single-tenant operations), the v1 architecture grows by a thin layer:

- **Principal field on every control-api request.** Populated by whatever auth mechanism the operator picks: mTLS subject DN, OIDC `sub` claim, JWT subject, mesh-injected sidecar header, etc. Rimsky is wire-agnostic — it accepts the principal field as opaque bytes/string and does not validate the auth mechanism itself (that's the operator's perimeter, same as today).
- **Pluggable policy hook.** A small interface the control-api consults for each operation: `can_register_template(principal)`, `can_instantiate(principal, template_name)`, `can_provision_substore(principal, store)`, `can_invalidate(principal, instance_id, node_id)`, etc. Default implementation: allow-all (so existing per-project deployments are unchanged). Built-in alternatives: ACL-based, role-based. Extension point for ops to bring their own.
- **Audit-log principal column.** Every state-mutating action records the calling principal alongside the existing action + timestamp + targets.

### 2.2 What this would not add

- Tenant identity. This is shape (1) → (2), not (1) → (3).
- Per-tenant template namespacing. Templates remain globally named within the deployment.
- Store-service-side principal awareness. The runtime stays auth-blind; principal stops at the control-api boundary.
- Cross-deployment auth. One rimsky's auth has nothing to do with another rimsky's auth.

### 2.3 Why this is cheap and high-value when it lands

- The schema delta is one column on a few tables (audit log primarily; possibly `rimsky_templates.created_by`, `rimsky_instances.created_by` for visibility).
- The control-api delta is the policy hook + a small principal-extraction middleware (configurable per auth mechanism).
- The runtime delta is zero. The auth-blind invariant holds.
- The value is real: enterprise deployments overwhelmingly want "can Bob deploy templates" / "can Alice run instances" / audit-trail-by-principal. They less often want tenant isolation.

### 2.4 When to pick this up

- A real shared deployment asks for it.
- The §2 registry-shape spec (in the control-layer doc) is being written and the policy-knob slot needs filling. (The slot is forward-compat; it can land as "always allow" in v1 and grow into the policy hook later.)
- Audit logging gets specified for any reason — the principal column is the natural extension.

The §2 work in this doc is mostly mechanical once a real customer requirement defines the policy semantics. It is not blocking v1.

## 3. Multi-tenancy: substrate-supported now, rimsky-enforced deferred

The 2026-05-01 conversation that produced this doc and the lifecycle-events reframing of `docs/2026-04-26-control-layer.md` §1 collapsed the multi-tenancy question into two distinct things, with very different cost profiles. They're worth distinguishing explicitly.

### 3.1 Substrate-supported multi-tenancy (available under v1)

The lifecycle-events model in control-layer §1 already lets operators run multi-tenant workloads against a single rimsky deployment, *without* rimsky-level tenant primitives. The substrate that makes this work:

- **Lifecycle events without template-side metadata.** Per spec §4.1 the lifecycle event proto carries only `template_id` and `instance_id`. Tenant identity is not a first-class envelope field; instead operators wire it through claim userdata (tenant-aware store-services may inspect it) or by deploying one store-service per tenant. The same multi-tenancy outcomes are reachable, but rimsky stays auth-blind.
- **Userdata is opaque end-to-end** (blessed invariant 11). Per-instance tenant identity carries through to executors via userdata; the executor uses tenant-scoped credentials baked in at registration time.
- **Stores config is operator-curated.** An operator can register one tenant-aware store-service that backs many tenants (e.g., a postgres store-service that uses `tenant_${tenant_id}_tpl_${template_id}` schemas), or register N per-tenant store-services and let templates pick the right one via per-instance params.
- **Substore lifecycle is store-internal.** Each store-service decides what events mean. A multi-tenant postgres store-service can implement `OnTemplateDeployed` to create per-tenant schemas, `OnTemplateUndeployed` to drop them per a per-tenant `auto_destroy` policy, etc.

What the operator gets:

- **Namespace isolation** between tenants (via the store-service's per-tenant schemas / prefixes / directories).
- **Credential isolation** when each tenant's executor invocation uses tenant-scoped credentials threaded through userdata.
- **Data isolation** when the tenant-aware store-service implements it correctly.

What the operator does *not* get:

- **Visibility isolation.** All templates, instances, and lifecycle log entries are visible to anyone who can query the control-api. There is no per-tenant filtering at the control-api level.
- **Resource isolation.** Tenants share scheduler ticks, supervisor heartbeats, the postgres state DB connection pool, etc. One tenant running hot affects others.
- **Cross-tenant audit views.** Audit logging in v1 doesn't carry principal or tenant; the operator's store-services and executors carry their own per-tenant audit if they want it.
- **Rimsky-enforced tenant invariants.** Trust shifts to the store-service. If the store-service routes incorrectly, two tenants can see each other's data; rimsky has no defense.

This is *operator-wired* isolation, not platform-enforced isolation. For workloads where the operator owns the whole stack — store-services, executors, and the rimsky deployment — it can be sufficient. For workloads where tenants don't trust the platform, it isn't.

### 3.2 Rimsky-enforced multi-tenancy (deferred)

The harder version: rimsky knows about tenants as a first-class concept and enforces isolation in the control-api and runtime. This is what the auth-doc previously called "build mode" and is what genuinely shared-multi-tenant deployments need when operators don't fully trust their own substrate. The delta over substrate-supported:

- **Tenant identity threaded through the schema.** `tenant_id` column on `rimsky_templates`, `rimsky_instances`, `rimsky_store_lifecycle`, `rimsky_dispatch`, `rimsky_nodes`, `rimsky_lock_holders`, audit log. Per-tenant uniqueness on template names. Tenant-scoped queries everywhere.
- **Tenant identity in the policy hook.** All §2 policy questions gain a tenant dimension: `can_register_template(principal, tenant_id)`, `can_instantiate(principal, tenant_id, template_name)`, etc. Cross-tenant operations are forbidden by default.
- **Tenant-aware Capabilities handshake.** Stores that participate in rimsky-enforced multi-tenancy declare their per-tenant support in the startup handshake; rimsky validates per-tenant invariants at startup.
- **Cross-tenant isolation invariants.** Every state-touching code path needs verification that it respects tenant scoping. Real test surface, including fault injection and DoS scenarios.
- **Cross-tenant DoS isolation.** One tenant overwhelming the scheduler, postgres connection pool, or supervisor heartbeat must not degrade others. May require per-tenant resource accounting and quota.
- **Per-tenant operator views.** Observability, audit, debugging — all need tenant scoping, plus a cross-tenant admin view.

### 3.3 Why rimsky-enforced is expensive

- Schema delta is substantial — tenant_id everywhere state lives.
- Every blessed invariant gets a tenant dimension to verify against.
- The auth-blind runtime invariant gets stretched: tenant identity has to flow into more decisions, even if the runtime itself doesn't introspect it.
- The lifecycle-events Capabilities handshake gains per-tenant nuance.
- Cross-tenant isolation testing is not cheap.

### 3.4 The honest alternative to rimsky-enforced

Run N rimsky deployments, one per tenant. Each is real isolation (separate processes, separate postgres). Operator IaC spins them up. The per-tenant cost is a postgres database + three small Go processes.

For enterprises that care about tenant isolation enough to ask, separate deployments are usually the right answer. Process-level isolation across deployments is stronger than software-enforced isolation in a single deployment.

### 3.5 When to pick up rimsky-enforced

- A real shared-multi-tenant customer asks for it, AND the cost of N rimskys is genuinely too high for them, AND substrate-supported multi-tenancy is provably insufficient (e.g., the operator does not control the store-services and cannot trust them to enforce isolation).
- A platform play where rimsky is itself the multi-tenant offering rather than infra.

Until then: substrate-supported (§3.1) for shared deployments where the operator owns the substrate, separate deployments for tenants that don't trust each other.

### 3.6 Forward-compat note

The §2 principal field and the §3.2 tenant_id additions are independent and additive. Doing nothing in v1 does not foreclose §2 or §3.2. Doing §2 in v1.x does not foreclose §3.2. Substrate-supported multi-tenancy (§3.1) is available under v1 as soon as the lifecycle-events model lands; it does not depend on §2 or §3.2.

## 4. Out of scope for this doc

- **Runtime orchestration.** See `docs/history/2026-04-25-stores-redesign-design.md` and the v3 update (`docs/history/2026-04-27-stores-redesign-v3-design.md`). The runtime is auth-blind.
- **Provisioning lifecycle.** See `docs/2026-04-26-control-layer.md` §1.
- **Template registry shape.** See `docs/2026-04-26-control-layer.md` §2.
- **Package distribution.** See `docs/2026-04-26-package-manager.md`. Package signing and trust live there; they're orthogonal to control-api auth.
- **Audit logging spec.** Mentioned here as the natural integration point for the principal field; the audit-log spec itself is a separate session under the control-layer doc's "other concerns" placeholders.
- **Credential rotation, federation, secrets management.** Operator concerns; rimsky inherits whatever the operator deploys.

## 5. Picking up where we left off

V1: nothing to do for auth. Document the per-project deployment stance in the operator guide. Keep `rimsky_templates` and friends free of principal/tenant columns.

V1, multi-tenancy (substrate-supported): falls out of the lifecycle-events spec in control-layer §1. No additional auth-doc work; the operator guide should document the substrate-supported multi-tenancy pattern (per-tenant store-services, tenant_id in claim userdata read by tenant-aware store-services, the visibility/resource/audit caveats listed in §3.1).

V1.x (when triggered by a real shared-deployment requirement): specify the principal field, the policy hook interface, the audit-log delta, and the registry-shape policy knob's allow-anyone / listed-principals / refuse semantics. Reference §2 of this doc.

V2 (only when substrate-supported multi-tenancy is provably insufficient for a real customer): specify the rimsky-enforced multi-tenancy delta. Reference §3.2 of this doc. Most of the design work is fresh; nothing already shipped should change in shape, only grow.
