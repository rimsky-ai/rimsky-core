# Rimsky Control Layer Design

## Status

- Design notes, 2026-04-26 (§1, §3, §4); §2 added 2026-05-01.
- Spun off from `docs/2026-04-25-store-redesign.md` (specifically §15 and §19.7, which were moved here so the workload-store + lock-primitive doc could stay focused on runtime concerns).
- Two substantive topics:
  1. **Provisioning on DAG instantiation** (§1) — design space mostly enumerated below; ready for spec conversion.
  2. **Template registry shape** (§2) — registry-only instantiation, registration paths, naming, lifecycle; settled in the 2026-05-01 conversation.
- Other control-layer concerns are listed as placeholders (§3); each earns its own session when prioritized.
- **Auth and multi-tenancy live in their own doc.** See `docs/2026-05-01-auth-and-multitenancy.md`. v1 stance: rimsky is per-project infra behind the operator's own auth perimeter; the control-api does not authenticate or authorize. References to "principal" or "tenant" in this doc are forward-compat hooks, not v1 features.
- **Scope boundary.** The runtime (scheduler, supervisor) is unchanged by everything in this doc. Runtime treats a store uniformly: a store is a store. Control-layer concerns — provisioning, audit logging, manifest scope enforcement — are the control-api's responsibility and shape what gets handed to the runtime, not how the runtime executes.

## Context

Rimsky's runtime — the scheduler tick + supervisor dispatch + executor protocol path — is well-defined by:

- `docs/specs/2026-04-25-stores-redesign-design.md` (workload stores, claim/lock model, attributes substitution, dispatch atomicity).
- `docs/specs/2026-04-26-frame-resolution-design.md` (frame as a first-class cascade primitive, per-template resolution mode).
- `docs/2026-04-25-store-redesign.md` §§4–19 (verb set refinement, capability struct cleanup, version elimination, empty-selector unification — soon-to-be-formalized as a third major rewrite of the workload-store contract).

The control-api's runtime-orchestration responsibilities — template deployment, instance creation, operator invalidates, admin endpoints — are part of those specs.

This doc is about **everything else** the control-api does. Specifically, the store-side state lifecycle around instance creation and the registry shape that templates land in. The architectural backbone:

- **Runtime sees a fully-resolved store.** A store registration in the runtime points at a store-service endpoint + namespace. The runtime never asks "where did this namespace come from."
- **Control-layer carves the namespace.** Whether at boot time from YAML, or at instance creation via a store-service admin operation, it's all control-layer work that produces the runtime-visible store config.
- **Store-services are stateless about bookkeeping.** The store is the ground truth on namespace existence. Rimsky's state DB tracks bookkeeping (which instance owns which namespace, what the auto-destroy policy is). Store-services execute store-kind-specific operations; they don't remember.

This separation is what lets the workload-store spec stay focused on runtime mechanics and what makes provisioning a separate spec. Auth and multi-tenancy are out of scope here — see `docs/2026-05-01-auth-and-multitenancy.md`.

---

## 1. Provisioning via store lifecycle events

> **Reframed 2026-05-01.** §1 originally described control-api-orchestrated provisioning with a separate admin verb set, a `rimsky_substores` namespace table, a per-store-kind matrix of namespace-naming conventions, and six pre-enumerated lifecycle shapes. That orchestration model has been replaced with the lifecycle-events model below. The new model pushes namespace-shape decisions into store-services (consistent with rimsky's auth-blind / project-agnostic / inert-runtime philosophy), shrinks the control-api's surface, and unlocks substrate-level multi-tenancy without rimsky-level tenant primitives. The old §1.4 matrix and `rimsky_substores` table are gone; what survives in shape is the cross-store partial-failure handling (§1.9) and the out-of-scope reminders.

### 1.1 The simple model

Templates declare substore dependencies. Rimsky signals lifecycle events to the relevant store-services at well-defined transitions; each store-service decides what those events mean. Rimsky stays inert — it does not orchestrate provisioning, does not know about namespace shapes, does not maintain a per-namespace bookkeeping table.

The substantive transitions and their events:

- **Template deployed.** A template that depends on store S registers. Rimsky fires `OnTemplateDeployed(store_registration_name, template_id)` to S. S decides what this means: provision a schema, allocate a directory, do nothing.
- **Template undeployed.** Rimsky fires `OnTemplateUndeployed(store_registration_name, template_id)`. S decides: drop the schema, leave it, archive it.
- **Instance created.** Rimsky fires `OnInstanceCreated(store_registration_name, template_id, instance_id)` for every store referenced by the template. S decides what an instance-scoped namespace means.
- **Instance terminated.** Rimsky fires `OnInstanceTerminated(store_registration_name, template_id, instance_id)`. S decides cleanup.

Rimsky never asks "what schema did you create" or "what directory does this map to." The store maintains its own internal mapping from scope IDs to namespaces. At runtime, the store consults that mapping when it receives an `Open` call.

### 1.2 Why this works

Any writeable store has SOME isolation primitive — schema, key prefix, directory, keyspace, branch. The events model lets each store-kind use its own primitive without rimsky knowing about it. Read-only stores implement the lifecycle events as no-ops.

There is no per-store "supports provisioning" capability flag. Lifecycle events are part of the standard wire protocol; stores that don't need to react to them don't.

### 1.3 The scope envelope on Open

The runtime side of the protocol grows a small standard envelope on `Open`:

```
Open(
  claim_id,
  store_registration_name,
  scope = { template_id, instance_id },
  spec
) → ClaimResult
```

The scope envelope carries the same identifiers rimsky used in lifecycle events. The store consults its internal mapping (populated during lifecycle-event handling) to resolve the scope to a namespace, then opens against that namespace. Stores with no per-template namespacing ignore the envelope.

The envelope is rimsky-inert: rimsky does not introspect, validate, or substitute its contents. Same pattern as userdata (blessed invariant 11) and claim content (blessed invariant 20).

### 1.4 Scope binding in the template manifest

The template manifest declares each substore dependency by name only:

```yaml
stores:
  - name: data
```

Both template-scope and instance-scope events fire for every store the template's nodes reference. Stores that need template-level setup react to `OnTemplateDeployed`; stores that need per-instance setup react to `OnInstanceCreated`. Stores with nothing to do at one (or both) scopes return `OK` and ignore the event.

There is no template-side `scope:` selector and no `metadata:` forwarding. Per-store knobs (auto-destroy, retention, multi-tenant routing) live in the store-service's own config, not in the template — option α in `docs/2026-05-01-auth-and-multitenancy.md`. Tenant identity, when the operator wants it, flows through claim userdata (which the store-service may inspect) or is encoded in the operator's choice of per-tenant store-service instances.

### 1.5 Per-store-kind shapes (illustrative)

Each store-service documents its own conventions in its operator guide. Rimsky does not standardize. A few illustrative shapes to show how varied the reactions can be while sharing the same protocol:

- **Postgres store-service.** On `OnTemplateDeployed`, creates a schema named per its own config (e.g., `tpl_${template_id}`), grants usage to its runtime role. On `OnInstanceCreated`, creates a per-instance schema. On `OnTemplateUndeployed` with `auto_destroy` set in the store-service's own config, drops the schema.
- **Filesystem store-service.** On `OnTemplateDeployed`, creates a subdirectory under its root. On undeploy, `rm -rf` if its own config's `auto_destroy` permits destruction.
- **S3 store-service.** Records the prefix mapping internally; provisioning is essentially free (prefixes don't need pre-creation). On undeploy with `auto_destroy`, schedules an async object-delete job; the event itself returns immediately.
- **Multi-tenant postgres store-service.** Reads tenant identity out of claim userdata at runtime and uses a `tenant_${tenant_id}_tpl_${template_id}` schema pattern. Operators who want tenant routing at the lifecycle layer instead deploy one store-service per tenant. The same store-service binary backs many tenants without rimsky knowing about tenants.
- **Read-only HTTP store-service.** Implements all lifecycle events as no-ops. Runtime `Open` connects to the configured upstream.

The pattern: stores choose how to interpret events based on their internal logic and config. Rimsky is uniform.

### 1.6 Bookkeeping: `rimsky_store_lifecycle`

Rimsky maintains a minimal log of events it has signaled, for idempotency on retry and so undeploy knows what events to fire:

```sql
CREATE TABLE rimsky_store_lifecycle (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    store_registration_name  TEXT NOT NULL,
    scope_kind               TEXT NOT NULL CHECK (scope_kind IN ('template','instance')),
    scope_id                 TEXT NOT NULL,         -- template_id or instance_id
    state                    TEXT NOT NULL CHECK (state IN ('deploying','deployed','undeploying','undeployed')),
    last_event_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (store_registration_name, scope_kind, scope_id)
);
```

This is bookkeeping for rimsky's event-firing — not for namespaces. The store-service is the ground truth on what got provisioned and what's still alive. Rimsky's table answers "did I send the deployed event yet" and "do I still need to send the undeployed event."

No `namespace` column, no `auto_destroy` flag, no scope_kind beyond template-vs-instance. Operator-managed knobs live in the template manifest's `metadata` block; namespace identifiers live inside the store-service.

### 1.7 Lifecycle event protocol

Lifecycle events are part of the standard store wire protocol, not a separate admin transport. They share auth, transport, and routing with the existing 4 runtime verbs (`Open` / `Commit` / `Abandon` / `Release`) plus the `Capabilities()` startup handshake.

The Capabilities handshake grows to declare which lifecycle events a store implements:

```
Capabilities {
  WriteSemantics: ...,
  LifecycleEvents: ['OnTemplateDeployed', 'OnTemplateUndeployed',
                    'OnInstanceCreated', 'OnInstanceTerminated']
}
```

Stores that don't implement an event omit it. Rimsky validates at startup that any template depending on an instance-scoped substore targets a store-service whose Capabilities includes the per-instance events.

If an operator wants admin-vs-runtime separation for network/firewall reasons, they configure their store-service to expose two endpoints sharing the same wire protocol; rimsky targets the admin endpoint. That's a deployment-time wiring choice, not a protocol design choice.

### 1.8 Quotas and capacity

Out of scope. Storage-level quotas live in the underlying technology and are enforced there. Stores that want to participate in quota enforcement do so internally (e.g., refusing `OnTemplateDeployed` when capacity is low). Rimsky stays inert.

### 1.9 Cross-store partial failures

A template depending on substores from 3 different store-services where 2 succeed and 1 fails: idempotent retry + opt-in cleanup.

- Each `OnTemplateDeployed` is retry-safe by `(store_registration_name, template_id)`. Stores already at `deployed` no-op on retry. Failed stores stay at `deploying`; control-api retries.
- On persistent failure, the operator either retries by hand (network blip) or fires undeploy on the partially-deployed template. Undeploy walks the `rimsky_store_lifecycle` log and signals undeploy to whichever stores reached `deployed` or `deploying`.
- No 2-phase commit. Each store's state is its own source of truth; rimsky's lifecycle log tracks which events got fired, not what got created.

### 1.10 What's deliberately out of §1's scope

- **Graph package install** as a separate gate. Template registration is the trigger for `OnTemplateDeployed`; there's no "install the package, then register the template later" two-step from rimsky's perspective. Package distribution is addressed elsewhere — see `docs/2026-04-26-package-manager.md`.
- **Per-region overrides on substores.** A template wanting one substore for hot data and another for cold just declares two store dependencies in the manifest — not a new feature.
- **Standardized per-store-kind naming patterns.** Each store-service owns its conventions and documents them in its operator guide. Rimsky never names a namespace.
- **Tenant identity as a first-class field on the scope envelope.** Tenant routing, when the operator wants it, flows through claim userdata (which the store-service may inspect) or is encoded in the operator's choice of per-tenant store-service instances. The store-service interprets it. Discussed further in `docs/2026-05-01-auth-and-multitenancy.md`.

---

## 2. Template registry shape

### 2.1 Registry-only instantiation

Every template lives in `rimsky_templates` under a name. `POST /v1/instances` references a template by name; there is no inline-spec path through dispatch. The runtime stays uniform — one lookup, one validation path, one store-dep resolution path.

The two-step shape (register, then instantiate) is a deliberate architectural commitment. It enables:

- **Stable template identity for substore scoping.** §1's lifecycle events carry `template_id` as the scope key; without a stable named template the events have no useful scope. Per-template (and operator-wired per-tenant) namespacing in store-services depends on rimsky having a registered template_id to fire events for.
- **Idempotent re-instantiation.** "Run the foo graph again with new params" and "submit a fresh spec that happens to look like foo" become structurally distinct requests. The registry path makes the distinction explicit.
- **Audit and governance.** Every running graph has a referent; audit log entries carry a name, not a content hash plus full-spec context.
- **Manifest scope enforcement.** Validation that template-referenced stores are declared (per the v3 stores spec §6.1) happens at registration time, not buried inside instance creation.

Direct inline instantiation is out of scope. The runtime accepts named templates only.

### 2.2 Registration paths

Registry-only does not mean every template arrives via the package manager. Two registration paths converge on the same `rimsky_templates` table:

1. **Direct registration.** `POST /v1/templates` with name, spec, and store-dependency declaration. Validated for syntax and against `RIMSKY_CONFIG`. Records who submitted it and when. No signing, no conformance, no lockfile.
2. **Package install.** The package manager (per `docs/2026-04-26-package-manager.md`) resolves dependencies, validates capabilities, provisions substores per §1, and *delegates to direct registration* under the hood — attaching package digest, publisher signature, conformance attestation, and lockfile pointer as additional metadata.

Both paths produce rows in the same table. The runtime cannot distinguish them at dispatch time. The control-api carries a per-deployment policy knob — accept-direct-from-anyone, accept-direct-from-listed-principals, or refuse-direct-entirely — that lets a hardened production deployment lock down to package-installed templates only, while a dev or air-gapped deployment runs entirely on direct registration. In v1 the knob is unconditionally "allow" (no auth); the listed-principals and refuse modes activate when the auth surface lands per `docs/2026-05-01-auth-and-multitenancy.md` §2.

### 2.3 The Docker analogy

The shape is structurally similar to Docker's local image registry:

- Docker maintains an internal image registry on every host. Every running container references an image by name + tag (or digest). There is no path that runs an image without it being in the registry.
- `docker-compose` deployments feel one-shot, but every image referenced still lands in the local registry — registration is automatic and invisible.
- A separate distribution layer (Docker Hub, private registries, image signing) layers on top. Pulling from a remote registry produces a local entry; users can also `docker build` locally with no remote involvement.

Rimsky should follow the same pattern. The registry is universal; the user-facing tooling can disguise it for one-shot deployments.

### 2.4 Names: auto-derived plus optional tags

Templates are named at registration. Two name sources, allowed to coexist on a single template:

- **Auto-derived from spec content.** Hash of the canonical spec serialization (e.g., `sha256-abc123…`). Reproducible — submitting the same spec twice produces the same name, making registration idempotent. Used when the spec carries no `name:` field and the CLI doesn't override.
- **User-given tag.** A `name:` field in the spec, or a `--name` flag at the CLI, supplies a human-readable name. Tags can move (re-tagging a content-addressed template is a registry operation, not a runtime concern).

A template carries a stable content hash as its canonical identity plus zero or more user-given tags pointing at that identity. Mirrors Docker's `image_id` (content hash) vs `image:tag` model.

Open question for the registry-shape spec session: whether the storage layout is hash-as-primary-key with tags as a separate index, or name-as-primary-key with content hash as a metadata field. The Docker model (content-addressed primary, named tags as references) is the cleaner one and is probably what falls out.

### 2.5 Lifecycle: leave them in the registry

Registered templates accumulate. Cleanup is a separate operator concern, not a runtime concern. Several mechanisms can coexist:

- **Manual deletion.** `rimsky-cli rm <name>` (or `DELETE /v1/templates/<name>`). Refused if any instance references the template; a force flag overrides.
- **TTL on unused templates.** A template with no instances and no recent activity older than a configurable threshold can be auto-deleted by a scheduler-driven sweeper. Default off; opt-in per deployment.
- **Garbage collection of dangling content-addressed templates.** Templates with no tags and no referencing instances are eligible for GC. Mirrors Docker's image GC.
- **Tag pruning.** Removing a tag does not delete the underlying content-addressed template; explicit deletion or GC does.

This removes any pressure to make the runtime's instantiation path "ephemeral-aware." The runtime never knows or cares whether a template was meant to be one-shot.

### 2.6 CLI shape (illustrative)

The dev-loop UX disguises the two-step:

- `rimsky-cli run ./graph.yml [--name foo] [--params …]` — registers the template (using `name:` from spec, `--name` flag, or content hash) and instantiates in one command. Default: leave both registered after the instance terminates.
- `rimsky-cli register ./graph.yml [--name foo]` — registration only.
- `rimsky-cli instantiate <template_name> [--params …]` — instantiation only.
- `rimsky-cli compose up ./graphs/*.yml` — multi-graph deployment from a directory; registers all and instantiates per a compose-style spec. Persistent by default.

The control-api sees the same `POST /v1/templates` and `POST /v1/instances` regardless of which CLI command produced them. Specifying the CLI surface in detail is a separate exercise; the architectural commitment is that registry-only is the only path through the control-api.

### 2.7 What's deliberately out of §2's scope

- **Inline-spec instantiation** as a runtime feature. Already settled: not a runtime concern. CLI-layer convenience replaces the need.
- **Storage layout and schema for `rimsky_templates`.** The control-api owns the table; the runtime reads template-by-name. The spec session settles the columns (content hash, tags, registration source, package-install metadata, timestamps). Principal/tenant columns are out of scope here — see `docs/2026-05-01-auth-and-multitenancy.md`.
- **The package-manager pipeline itself.** Covered by `docs/2026-04-26-package-manager.md`. This section's contract with that doc is "package install delegates to direct registration with extra metadata"; the package manager does not get its own path through the control-api.
- **Multi-tenant template namespacing.** Whether templates are per-tenant, globally shared, or both is an auth + tenancy concern; see `docs/2026-05-01-auth-and-multitenancy.md` §3.

---

## 3. Other control-layer concerns (placeholders)

These earn their own focused sessions when prioritized. Listed here so the control-layer architecture has a complete map.

- **Instance lifecycle beyond create/destroy.** Pause, resume, snapshot, migration. Mostly v2; no immediate need but worth a placeholder.
- **Audit logging.** Admin operations, provisioning, destruction — all need a durable audit trail. Probably a `rimsky_audit_log` table with control-layer-emitted entries. The principal column lands as part of `docs/2026-05-01-auth-and-multitenancy.md` §2 when that surface arrives; v1 audit entries record action + timestamp + targets only.
- **Manifest scope enforcement.** Already mostly shipped via stores-redesign (graphs declare store deps; runtime claims validated). Spec session confirms end-to-end coverage; no new design likely needed.
- **Quota enforcement (control-layer participation).** §1.8 punts this; if it becomes a real concern, the design is "control-api refuses provisioning when quota would exceed; supervisor refuses dispatch when quota would exceed."
- **Versioning of manifests / graph dependencies.** When a graph package's store dependencies change between versions, what happens to existing instances? Is this a control-layer concern or a package-manager concern?

---

## 4. Out of scope for this doc

- **Runtime orchestration.** See `docs/specs/2026-04-25-stores-redesign-design.md` and `docs/specs/2026-04-26-frame-resolution-design.md`.
- **The workload-store + lock-primitive design.** See `docs/2026-04-25-store-redesign.md` (especially §19 for the post-implementation resolutions).
- **Package install / distribution.** See `docs/2026-04-26-package-manager.md`. Package install is a separate concern from instance creation; this doc treats instance creation as the provisioning trigger.
- **Frame resolution.** See `docs/2026-04-26-frame-resolution.md` and `docs/specs/2026-04-26-frame-resolution-design.md`. Frames are a runtime primitive; control-layer concerns are orthogonal.
- **Auth and multi-tenancy.** See `docs/2026-05-01-auth-and-multitenancy.md`. v1 stance: per-project deployment, no rimsky-side auth; principal/tenant fields are forward-compat hooks not features.

---

## 5. Picking up where we left off

Two paths from this doc to formal specs:

1. **Lifecycle-events provisioning spec (§1).** Strategy is settled (events on the standard wire protocol, `rimsky_store_lifecycle` log, scope envelope of `template_id` + `instance_id` only — no template-side metadata block). Spec work:
   - Specify the wire signatures of `OnTemplateDeployed` / `OnTemplateUndeployed` / `OnInstanceCreated` / `OnInstanceTerminated` on `proto/v1/store.proto`.
   - Specify the scope envelope addition to `Open`.
   - Specify the `Capabilities.LifecycleEvents` extension and the rimsky-side startup validation (template substore declarations vs. store-service capabilities).
   - Specify the `rimsky_store_lifecycle` schema and migrations.
   - Specify the manifest declaration syntax for substore dependencies (`scope: template | instance`, `metadata: { … }`).
   - Specify the control-api transitions that fire events (template registration, template unregistration, instance creation, instance termination) and how partial-failure retries work.
   - Specify failure-handling semantics for cross-store partial deployments.
   - Out-of-scope reminder: quotas, capacity planning, cross-store 2PC, per-store-kind canonical naming.
2. **Registry-shape spec (§2).** Strategy is settled; spec work:
   - Specify the `rimsky_templates` schema (content hash, tags, registration source, package-install metadata, timestamps; principal/tenant columns deferred to the auth doc).
   - Specify the `POST /v1/templates`, `GET /v1/templates/<name>`, `DELETE /v1/templates/<name>` endpoints and the tag-management endpoints.
   - Specify the canonical spec serialization for content hashing (deterministic, stable across cosmetic changes).
   - Specify the TTL-sweeper and GC semantics for unused templates.
   - Specify the policy-knob slot for accept-direct-registration (v1 hardcoded "allow"; the listed-principals and refuse modes activate per `docs/2026-05-01-auth-and-multitenancy.md` §2).
   - Specify the contract between `rimsky-cli register/run/instantiate/compose` and the control-api.

Both are independent of the auth surface. The auth doc's §2 lands a thin layer on top when a real shared-deployment requirement appears.
