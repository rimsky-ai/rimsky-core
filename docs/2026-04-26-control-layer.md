# Rimsky Control Layer Design

## Status

- Design notes, 2026-04-26.
- Spun off from `docs/2026-04-25-store-redesign.md` (specifically §15 and §19.7, which were moved here so the workload-store + lock-primitive doc could stay focused on runtime concerns).
- Two substantive topics:
  1. **Provisioning on DAG instantiation** (§1) — design space mostly enumerated below; ready for spec conversion.
  2. **Control-layer auth** (§2) — placeholder for a focused future session.
- Other control-layer concerns are listed as placeholders (§3); each earns its own session when prioritized.
- **Scope boundary.** The runtime (scheduler, supervisor) is unchanged by everything in this doc. Runtime treats a store uniformly: a store is a store. Control-layer concerns — provisioning, auth, tenant management, audit logging, manifest scope enforcement — are the control-api's responsibility and shape what gets handed to the runtime, not how the runtime executes.

## Context

Rimsky's runtime — the scheduler tick + supervisor dispatch + executor protocol path — is well-defined by:

- `docs/specs/2026-04-25-stores-redesign-design.md` (workload stores, claim/lock model, attributes substitution, dispatch atomicity).
- `docs/specs/2026-04-26-frame-resolution-design.md` (frame as a first-class cascade primitive, per-template resolution mode).
- `docs/2026-04-25-store-redesign.md` §§4–19 (verb set refinement, capability struct cleanup, version elimination, empty-selector unification — soon-to-be-formalized as a third major rewrite of the workload-store contract).

The control-api's runtime-orchestration responsibilities — template deployment, instance creation, operator invalidates, admin endpoints — are part of those specs.

This doc is about **everything else** the control-api does. Specifically, the substrate-side state lifecycle and the auth mechanics that surround instance creation. The architectural backbone:

- **Runtime sees a fully-resolved store.** A store registration in the runtime points at a substrate connection + namespace. The runtime never asks "where did this namespace come from."
- **Control-layer carves the namespace.** Whether at boot time from YAML, or at instance creation via a bridge admin operation, it's all control-layer work that produces the runtime-visible store config.
- **Bridges are stateless.** Substrate is ground truth on namespace existence. Rimsky's state DB tracks bookkeeping (which instance owns which namespace, what the auto-destroy policy is). Bridges execute substrate-specific operations; they don't remember.

This separation is what lets the workload-store spec stay focused on runtime mechanics and what makes provisioning/auth/multi-tenancy a separate spec.

---

## 1. Provisioning on DAG instantiation

### 1.1 The simple model

Instance creation is the provisioning trigger. When `POST /v1/instances` arrives at control-api with a graph instance request, control-api:

1. Reads the graph's store dependencies (declared in the template).
2. For each dependency that requires provisioning, calls the bridge's substrate-specific namespace operation.
3. Records the provisioned namespace in `rimsky_substores`.
4. Registers a runtime store config that points at the provisioned namespace (substrate connection + namespace identifier).
5. Proceeds with normal instance creation — emits `rimsky_nodes` rows, frames, etc. — referring to the freshly-registered store by name.

The runtime sees a store registration indistinguishable from one ops carved by hand at boot time. **No new runtime concept; no new capability flag; no new dispatch-time lookup.**

### 1.2 Why "any writeable store" supports this

If a substrate accepts new state, it has SOME isolation primitive — schema, key prefix, directory, keyspace, branch — that can be used to carve a namespace. The substrates that don't support provisioning are read-only sources, and they don't need it.

So provisioning isn't a per-store capability flag. It's a baseline expectation for writeable stores. The interesting work is per-substrate: how does each substrate's isolation primitive express each lifecycle shape?

### 1.3 Use cases (design-space enumeration)

Six reasonable shapes for substore lifecycle:

1. **Per-instance, ephemeral.** New namespace per instance; destroyed on uninstall. The default safe case — namespace is exactly co-terminous with the instance.
2. **Per-instance, persistent.** New namespace per instance; outlives uninstall. The instance was a workload over data that should remain accessible after the workload completes.
3. **Per-DAG, shared across instances.** All instances of DAG X share one namespace. Useful for singleton-ish workloads, shared queues consumed by multiple instance copies, or replicated execution where the data is shared on purpose.
4. **Per-tenant, shared across DAGs.** Tenant identifier carves the namespace; multiple DAGs and their instances within the tenant share. Useful for multi-customer SaaS deployments where each customer is a tenant.
5. **Global / ops-configured.** No provisioning. Ops carved the namespace via YAML at boot time. Already supported; mentioned for completeness.
6. **External pre-existing.** Instance binds to a namespace that ops already created out-of-band (not in YAML, but via a separate operator process). No provisioning either, but the binding produces a runtime store registration.

The graph manifest declares which shape each store dependency wants. Default is per-instance ephemeral — destroying the namespace when nothing references it is the safe default; persistent and shared shapes are explicit opt-in.

### 1.4 Per-substrate "store rules" matrix

How each substrate expresses each lifecycle shape:

| Substrate | Isolation primitive | Per-instance | Per-DAG | Per-tenant |
|---|---|---|---|---|
| Postgres | Schema | `inst_${instance_id}` | `dag_${dag_name}` | `tenant_${tenant_id}` |
| S3 | Key prefix | `${bucket}/inst/${instance_id}/` | `${bucket}/dag/${dag_name}/` | `${bucket}/tenant/${tenant_id}/` |
| Filesystem | Subdirectory | `${root}/inst/${instance_id}/` | `${root}/dag/${dag_name}/` | `${root}/tenant/${tenant_id}/` |
| Redis | Keyspace prefix | `inst:${instance_id}:` | `dag:${dag_name}:` | `tenant:${tenant_id}:` |
| Git | Branch / subdir | branch `inst/${instance_id}` | branch `dag/${dag_name}` | dir `tenant/${tenant_id}/` |

The naming conventions above are illustrative. Spec session lands on canonical formats per substrate; bridges document their format in their manifest.

Substrate-specific quirks worth highlighting:

- **Postgres schemas.** Need `GRANT USAGE ON SCHEMA … TO ${runtime_role}` after creation. Shared (per-DAG, per-tenant) schemas need permission scoping such that two tenant-X instances can both read/write but tenant-Y can't see them.
- **S3 key prefixes.** No "delete the prefix" operation — uninstall must list and delete. For large prefixes this is slow and partially expensive. Spec session decides whether uninstall is synchronous (wait for full delete) or asynchronous (mark the binding as destroyed; let a background sweeper clean up).
- **Filesystem subdirectories.** `rm -rf` is fast but irreversible. Per-instance-ephemeral with `auto_destroy: true` means deleting on uninstall; spec needs explicit confirmation flow for irreversible cases.
- **Redis keyspaces.** Prefix is convention; not enforceable. Two graphs with the same prefix WILL collide. Bridge needs a prefix-uniqueness invariant, possibly via a registry inside Redis itself (a metadata key that lists provisioned prefixes).
- **Git.** Branch-vs-subdir is a real fork. Branches give per-instance history isolation; subdirs share history but isolate working tree. Different workloads want different shapes; spec might ship both.

### 1.5 Lifecycle hooks

For each provisioning operation, the control-api flow:

- **Create namespace.** Substrate-specific. Bridge holds admin credentials; carves the isolation primitive.
- **Grant runtime access.** Substrate-specific. Bridge configures permissions so the runtime user (or a per-instance scoped credential) can read/write the namespace.
- **Bind to runtime store registration.** Common across substrates. Produces the runtime-visible store config (substrate connection details + namespace identifier) and registers it in the runtime's store registry.
- **Destroy on uninstall.** Substrate-specific. `DROP SCHEMA`, S3 prefix delete, `rm -rf`, `DEL` keys, branch delete.
- **Idempotency.** Re-instantiation must find an existing namespace and reuse it rather than fail. The control-api's `rimsky_substores` table provides the lookup; if a row exists for the (parent_store, scope_kind, scope_id) tuple, reuse it.

### 1.6 Bookkeeping: `rimsky_substores`

A new state table tracks provisioned namespaces:

```sql
CREATE TABLE rimsky_substores (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_store    TEXT NOT NULL,            -- the bridge / parent substrate's registered name
    namespace       TEXT NOT NULL,            -- substrate-specific identifier
    scope_kind      TEXT NOT NULL CHECK (scope_kind IN ('instance','dag','tenant','external')),
    scope_id        TEXT NOT NULL,            -- instance_id, dag_name, tenant_id, or external-binding-id
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    auto_destroy    BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE (parent_store, scope_kind, scope_id),
    UNIQUE (parent_store, namespace)
);
```

Read by control-api during install (idempotency check) and uninstall (cleanup orchestration); not read by runtime. The substrate is ground truth on namespace existence; this table is the bookkeeping layer that lets control-api know what to clean up later, what scope a namespace belongs to, and whether to destroy on uninstall.

### 1.7 Bridge admin verbs

To execute provisioning, bridges expose substrate-specific admin operations control-api invokes:

```
ProvisionNamespace(name, scope) → NamespaceID
DestroyNamespace(id) → ()
ListNamespaces() → [NamespaceID]    # diagnostic / cleanup
```

Three verbs. Authenticated as control-api (admin role); separate transport from runtime endpoints — different port or different gRPC service so ops can firewall-restrict admin access to control-api only. Auth specifics are §2.

The runtime endpoint of the bridge (the verb set per `docs/2026-04-25-store-redesign.md` §19.3 — `ResolveRegion`/`Allocate`/`Commit`/`Abandon`/`Delete` + optional read-lease) is unchanged by provisioning. Provisioning happens through the admin endpoint; runtime ops happen through the runtime endpoint.

### 1.8 Quotas and capacity

Out of scope for v1. Substrate-side quotas (postgres connection limits, S3 storage caps, filesystem disk space) are operator concerns and are enforced by the substrate when reached. If the orchestrator needs to participate in quota enforcement (refuse to provision when capacity is low, throttle writes near limits), that's a v2 extension.

The graph manifest can declare expected resource usage as an informational hint (e.g., `expected_storage_bytes_max: 50GB`) for operator capacity planning, but the control-api doesn't enforce it.

### 1.9 Cross-substrate transactional install

A graph with substores from 3 different bridges where 2 succeed and 1 fails: handled by **idempotent retry + opt-in cleanup**.

- Install is retry-safe by (parent_store, scope_kind, scope_id). On failure, re-running install picks up where it left off.
- Cleanup is explicit: `POST /v1/instances/{id}/uninstall` walks the `rimsky_substores` rows for this scope and calls `DestroyNamespace` per bridge.
- No 2-phase commit. The substrate-side state is the source of truth; the bookkeeping is best-effort eventual.

### 1.10 What's deliberately out of §1's scope

- **Graph package install** as a separate gate. Instance creation IS the trigger; there's no "install the package, then create instances later" two-step. Package distribution and graph composition are addressed elsewhere — see `docs/2026-04-26-package-manager.md` for the package-manager design.
- **Per-region overrides on substores.** A graph might want one substore for hot data and another for cold. That's just declaring two store dependencies in the manifest — not a new feature.

---

## 2. Control-layer auth

### 2.1 Status: deferred to a focused future session

Auth is non-trivial and earns its own discussion. This section is a placeholder for that session — it lists the boundaries auth touches and the topics worth committing to during the focused session.

### 2.2 Auth boundaries the control layer touches

The auth landscape relevant to control-api:

| Boundary | What flows | Mechanism (today's expectation) |
|---|---|---|
| Operators → control-api | Admin commands (deploy template, create instance, force-fire, kill, query state) | Operator deployment auth (mTLS, service mesh, IAM, JWTs) |
| End users → control-api | Workload commands (invalidates, observe state) | Same as operators, with a possible role separation |
| Control-api → bridges (admin endpoint) | Provisioning operations | Admin-role credentials; firewalled to control-api only |
| Control-api → bridges (runtime endpoint) | N/A | Runtime path is supervisor → bridge, not control-api |
| Tenant identity flow | Multi-tenant deployments need tenant identity threaded through instance creation | Manifest declaration + request header + scope enforcement |
| Manifest scope enforcement | Graphs declare store dependencies; runtime claims must be within declared scope | Mostly already shipped via stores-redesign; spec session confirms coverage |

### 2.3 Topics for the focused session

- **Authentication mechanisms.** mTLS, service mesh, IAM, JWTs, opaque tokens. Pick a recommended default + a "bring your own" extension point.
- **Tenant identity propagation and isolation.** How tenant identity arrives in an instance-creation request, how it flows to the bridge for namespace selection, how it's enforced at runtime claim time.
- **Credential rotation and federation.** Bridge admin creds rotated by ops; control-api credentials rotated by ops; runtime user credentials managed per-substore.
- **Audit logging of admin operations.** Durable trail of who provisioned/destroyed what, when.
- **Relationship to the auth-blind philosophy** in `docs/2026-04-25-store-redesign.md` §14: the runtime is auth-blind (transports bytes, doesn't understand auth); the control layer IS auth-aware (authenticates clients, holds bridge admin creds, enforces scope). The boundary is precise — runtime never sees auth state; control-layer never reaches into runtime decisions. Spec needs to formalize the boundary.

---

## 3. Other control-layer concerns (placeholders)

These earn their own focused sessions when prioritized. Listed here so the control-layer architecture has a complete map.

- **Instance lifecycle beyond create/destroy.** Pause, resume, snapshot, migration. Mostly v2; no immediate need but worth a placeholder.
- **Audit logging.** Admin operations, provisioning, destruction, auth events — all need a durable audit trail. Probably a `rimsky_audit_log` table with control-layer-emitted entries.
- **Manifest scope enforcement.** Already mostly shipped via stores-redesign (graphs declare store deps; runtime claims validated). Spec session confirms end-to-end coverage; no new design likely needed.
- **Tenant management.** If multi-tenancy is a first-class feature, tenant CRUD, resource accounting per tenant, tenant-scoped operator views. Probably a spec session of its own.
- **Quota enforcement (control-layer participation).** §1.8 punts this; if it becomes a real concern, the design is "control-api refuses provisioning when quota would exceed; supervisor refuses dispatch when quota would exceed."
- **Versioning of manifests / graph dependencies.** When a graph package's store dependencies change between versions, what happens to existing instances? Is this a control-layer concern or a package-manager concern?

---

## 4. Out of scope for this doc

- **Runtime orchestration.** See `docs/specs/2026-04-25-stores-redesign-design.md` and `docs/specs/2026-04-26-frame-resolution-design.md`.
- **The workload-store + lock-primitive design.** See `docs/2026-04-25-store-redesign.md` (especially §19 for the post-implementation resolutions).
- **Package install / distribution.** See `docs/2026-04-26-package-manager.md`. Package install is a separate concern from instance creation; this doc treats instance creation as the provisioning trigger.
- **Frame resolution.** See `docs/2026-04-26-frame-resolution.md` and `docs/specs/2026-04-26-frame-resolution-design.md`. Frames are a runtime primitive; control-layer concerns are orthogonal.

---

## 5. Picking up where we left off

Two paths from this doc to formal specs:

1. **Provisioning spec (§1).** Most of the design space is enumerated. Spec work:
   - Settle the canonical naming formats per substrate (the §1.4 matrix is illustrative).
   - Specify the `rimsky_substores` schema and migrations.
   - Specify the `ProvisionNamespace`/`DestroyNamespace`/`ListNamespaces` admin verb set on the bridge protocol.
   - Specify the control-api endpoints (`POST /v1/instances` already exists; provisioning is a phase inside it).
   - Specify the manifest declaration syntax for store-dependency scope.
   - Per-substrate "store rules" sections — postgres/s3/filesystem/redis/git in detail.
   - Out-of-scope reminder: quotas, capacity planning, cross-substrate 2PC.
2. **Auth spec (§2).** Largely a fresh design effort. Spec work depends on the focused session that picks the recommended auth mechanism and tenant model.

The §1 work is independent of §2; provisioning can be specified and shipped without auth being settled (provisioning works against bridge admin creds that are wired in deployment config; the control-api → bridge admin auth itself is a deployment concern). §2's scope is when those auth concerns become orchestrator-side feature, not just deployment config.
