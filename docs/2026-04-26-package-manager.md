# Package Manager Design

## Status

- Design notes, 2026-04-26.
- Companion to:
  - `docs/specs/2026-04-25-stores-redesign-design.md` — the landed stores redesign (foundation; templates, attributes, locks, claim stores, the `core/store/` interface).
  - `docs/2026-04-25-store-redesign.md` — store ecosystem and lock primitive refinement (provides context on bridges, the auth-blind philosophy, and multi-tenant stores).
- Captures the package manager design as worked through in conversation.
- **Sub-graph composability is deliberately deferred** to a separate session. Local graph composition (within a single deployment) should be designed first; package-level graph composition is an add-on after that.
- Authored as conversation notes for use in a future session. Not blocking any in-flight implementation work; the design captures decisions made now so they're not re-litigated later.

## Context

Rimsky users will eventually want to share workloads — graph specs that solve problems, executors that run them, stores that back them. The question is: what shape does that sharing take, and what does the platform need to provide?

This conversation worked through three constraints that ruled out several plausible designs:

1. **Don't reinvent IaC.** k8s, Terraform, Helm, and CloudFormation already handle infrastructure provisioning; a Rimsky package manager that tries to compete with them would be a bad reimplementation of a much harder problem. The package manager must stay on the data side of the IaC boundary.

2. **Three artifact types, three distribution shapes.** Graph specs are pure data; executors and stores are OCI images that map to running infrastructure. A single distribution mechanism would either over-fit graphs (making them unnecessarily heavy) or under-fit infrastructure (failing to convey deployment requirements).

3. **Multi-stakeholder workflow.** A Rimsky deployment serves at least four roles (platform ops, workload ops/admin, graph author, graph instantiator). Each role's needs from the package manager differ. A design that ignores this collapses into "everyone does everything" and matches no real org.

What this doc covers:

- The three-artifact catalog model (Option C from the conversation).
- The local DAG registry workflow (Option D), built on top of C.
- Roles and lifecycle phases.
- Integration with multi-tenant stores (per `docs/2026-04-25-store-redesign.md` §15).
- Versioning, trust, install flow.

What this doc deliberately does not cover:

- Real infrastructure provisioning (ops + IaC).
- Sub-graph composability (deferred to a future session — local first).
- Specific registry implementation (OCI is the obvious answer; specifics later).
- Discovery UX, search, ratings, public marketplace mechanics.

## 1. Roles and lifecycle

A non-trivial Rimsky deployment has at least four distinguishable roles:

1. **Platform ops.** Provisions Rimsky itself (scheduler, supervisor, control-api, postgres state DB) using the org's IaC tool. Doesn't think about graphs or workloads.

2. **Workload ops / admin.** Provisions the executors and stores that workloads need. Registers them with Rimsky's control-api. Curates the set of allowed packages. Decides which graphs the deployment supports.

3. **Graph author.** Writes graph specs. Publishes them as packages (potentially to a public registry, potentially to an org-internal one). May also author executors and stores when needed.

4. **Graph instantiator.** Submits instances of registered graphs via control-api. End user.

Smaller deployments collapse roles (one engineer plays platform ops, workload ops, and graph author). Larger deployments separate them rigidly with different access controls. The design must work at both ends.

The corresponding lifecycle phases:

1. **Platform provisioning.** Ops uses IaC to bring up Rimsky.
2. **Workload-infra provisioning.** Ops uses IaC to bring up executors and stores; registers them with Rimsky.
3. **Graph package install.** Admin (or self-service user) installs a graph package; deps validated; sub-stores provisioned; spec registered with control-api.
4. **Graph instantiation.** User submits an instance via control-api; runtime begins.

A package manager that ignores this tiering ends up trying to do all four at once and lands in CloudFormation territory. The design here is strict about which phase the package manager participates in (phase 3, with catalog support for phases 2 and earlier).

## 2. The data vs infrastructure boundary

The artifact types differ fundamentally in their nature and provisioning cost:

| Artifact | Nature | Provisioning | Distribution |
|---|---|---|---|
| Graph spec | Pure data (YAML / structured config) | Free — just register | Trivial; file or registry blob |
| Executor | OCI image + runtime container | Compute resources | OCI registry |
| Store / bridge | OCI image + runtime container, OR external substrate (managed S3, hosted postgres) | Compute + storage + auth | OCI registry |

Graphs are free to install. Executors and stores cost real resources.

**A package manager that provisions resources is reinventing IaC. A package manager that just publishes artifacts and validates compatibility is doing the right thing.**

The design here keeps the package manager strictly on the data side:

- It **distributes** graph specs (data).
- It **catalogs** executor and store packages (image references + manifests).
- It **validates** dependency and capability matching at install time.
- It **provisions logical sub-namespaces** within already-provisioned multi-tenant stores (§4).
- It **does not** deploy containers, allocate storage, manage IAM, or provision networking. Those are ops + IaC.

The line between "provisioning logical namespaces" and "provisioning infrastructure" is where multi-tenancy lives — see §4.

## 3. Three-artifact catalog (Option C)

The catalog supports three artifact types, each with a consistent manifest shape.

### 3.1 Graph package

A graph package is the actual graph spec plus a dependency declaration. Install registers the spec with Rimsky's control-api template registry.

```yaml
package:
  name: standard-ingest-pipeline
  version: 1.2.0
  type: graph
  publisher: example.com/datateam
  license: Apache-2.0

dependencies:
  executors:
    - type: llm-agent
      protocol: v1
      version: ">=2.0"
  stores:
    - kind: filesystem
      capabilities:
        read_during_write: async
    - kind: claim_store
      capabilities:
        on_commit: delete

substores:
  - logical_name: workspace
    parent_kind: filesystem
    quota:
      bytes_max: 50GB
    auto_destroy: true
  - logical_name: data
    parent_kind: postgres
    capabilities_required:
      read_during_write: async
    quota:
      rows_max: 10_000_000
    auto_destroy: false

spec:
  templates:
    - type: ingest
      executor: llm-agent
      stores:
        - { name: workspace, write: ["**"] }
        - { name: data, write: ["raw_input"] }
      ...
```

Key points:

- **Dependencies are declared by capability**, not by specific package version. The graph says "I need an llm-agent executor implementing protocol v1, ≥2.0"; the deployment provides whatever package satisfies that.
- **Sub-stores are first-class manifest content** (per `docs/2026-04-25-store-redesign.md` §15). The package manager provisions them at install time.
- **The spec is the graph definition** — what would today be a Rimsky template.
- **No infrastructure references**. The graph never names a specific bridge instance, postgres cluster, or container; it names a *kind* and a *capability requirement*.

### 3.2 Executor package

An executor package is a pointer to an OCI image plus a capability and registration manifest. **Catalog only — installation does not deploy.**

```yaml
package:
  name: claude-agent
  version: 2.5.1
  type: executor
  publisher: fallguy.com
  license: MIT

image: ghcr.io/fallguy/claude-agent:2.5.1@sha256:abc...

provides:
  type: llm-agent
  protocol: v1
  features: [resumable, async-callback]

registration:
  schema:
    ANTHROPIC_API_KEY: { type: string, secret: true }
    MAX_CONCURRENT: { type: int, default: 10 }
    LOG_LEVEL: { type: string, default: "info", enum: ["debug", "info", "warn", "error"] }

conformance:
  last_passed: 2026-04-26
  suite_version: 1.3
  result_url: https://example.com/conformance/claude-agent/2.5.1.json
```

Key points:

- **The image is a digest-pinned OCI reference.** Operators pull the image with their existing tooling.
- **`provides` declares the capability** the executor offers. Graph dependencies (`type: llm-agent`) match against this.
- **`registration.schema` documents what the operator must supply** when bringing up an executor instance and registering it with Rimsky. This becomes input to the operator's IaC.
- **`conformance` records test results** so graph authors can trust capability claims. Stale conformance is a deployment-time warning.

Operators consume an executor package by:

1. Pulling the image (`docker pull ghcr.io/fallguy/claude-agent:2.5.1`).
2. Deploying it with their IaC, supplying values for `registration.schema`.
3. Running `rimsky-cli register-executor --package claude-agent@2.5.1 --instance-name claude-prod --endpoint <gRPC URL>` to bind the running container to Rimsky.

The package manager doesn't do steps 1 or 2. It does step 3 (or provides the manifest input for the operator's IaC to do step 3 declaratively).

### 3.3 Store package

A store package is similar to an executor package — image reference plus capability and registration manifest.

```yaml
package:
  name: postgres-multitenant-bridge
  version: 1.0.0
  type: store
  publisher: fallguy.com
  license: Apache-2.0

image: ghcr.io/fallguy/store-postgres-bridge:1.0.0@sha256:def...

provides:
  kind: postgres
  capabilities:
    read_during_write: async
    versioning_model: mvcc
    supports_provisioning: true
    provisioning_unit: schema
    quota_dimensions: [rows_max, schema_size_bytes_max]

registration:
  schema:
    DSN: { type: string, secret: true }
    ADMIN_DSN: { type: string, secret: true, description: "credential with CREATE SCHEMA" }
    DEFAULT_SCHEMA: { type: string, default: "rimsky_data" }

conformance:
  last_passed: 2026-04-26
  suite_version: 1.0
```

Key points:

- **Same shape as executor packages.** Image, capabilities, registration schema, conformance.
- **Multi-tenancy is declared via capability** (`supports_provisioning: true`, `provisioning_unit: schema`).
- **Inlined stores** (those compiled into Rimsky proper) follow the same manifest pattern but reference a Go module instead of an OCI image. They register with the control-api at platform startup rather than at deploy time.

The catalog is uniform across executors and stores. The only difference is the runtime contract (executor protocol vs. store protocol).

### 3.4 Why three types under one roof

The package manager treats all three under one roof because:

- **Cross-references.** A graph package references executors and stores by capability; users want to discover all three from the same surface.
- **Consistent vocabulary.** Capabilities, registration schemas, and conformance results have the same shape across the three. One set of validators.
- **Single trust model.** Signing, allow-listing, and provenance work uniformly.

Two distinct distribution channels would force duplicate tooling for the same problem.

## 4. Multi-tenant store integration

Per `docs/2026-04-25-store-redesign.md` §15, multi-tenant bridges support sub-store provisioning via an admin verb set. The package manager is the natural orchestrator for this.

When installing a graph package that declares `substores`:

1. **Find a registered multi-tenant bridge** of each required `parent_kind` whose capabilities meet the graph's `capabilities_required`. Fail fast if none matches.
2. **Validate quota against parent capacity** (bridge's `GetSubstoreUsage` plus configured pool capacity).
3. **Call `ProvisionSubstore`** on each bridge with the requested name, capabilities, and quota. Receive a `SubstoreID` per provisioned sub-store.
4. **Register each sub-store** as a regular store in the control-api, scoped to this graph instance.
5. **Bind logical names** (`workspace`, `data`) to the provisioned `SubstoreID`s.
6. **Persist the binding** in the control-api's per-instance store registry.

On uninstall, for sub-stores with `auto_destroy: true`:

1. Call `DestroySubstore(id)` on the bridge.
2. Remove the binding from the registry.

This is **logical provisioning** — namespacing inside already-provisioned infrastructure. The IaC boundary holds:

- **Real infrastructure** (compute, storage, IAM, network) → ops + IaC.
- **Logical sub-namespaces** (schemas, prefixes, directories) → bridge admin verbs invoked by the package manager at install.

Critically: nothing the package manager does at install time can fail because of missing real-world resources. If the parent substrate is too small, that fails at quota validation (early, with a clear error). If the bridge is missing, that fails at capability matching (early, also clear). Real infrastructure costs are bounded by what ops already provisioned.

## 5. Local DAG registry (Option D)

Layered on top of the catalog, a **local DAG registry** is the recommended deployment pattern for any non-trivial Rimsky operation.

### 5.1 The pattern

Public packages exist; the deployment doesn't trust them blindly. Workload-ops admins curate an internal library:

- Pull candidate packages from public sources.
- Review them (security, capability claims, license).
- Test them against the deployment's executors and stores.
- Add approved packages to the local registry.
- Users instantiate from the local registry only.

The local registry is a private OCI registry (or any artifact store) maintained by the org. The Rimsky package manager points at it as the default source; public registries are optional secondary sources requiring explicit allow.

### 5.2 What this gives operators

- **Pre-vetted compatibility.** Every package in the local registry is known to work with the deployment's stack.
- **No surprise resource asks.** Packages are reviewed for sub-store quotas, executor counts, etc. before they're available to users.
- **Easier governance.** Compliance, license tracking, vulnerability scanning happen at the curation layer.
- **Forking flexibility.** When a public package needs modification, the org forks it, modifies it, and ships their fork to the local registry. Users see only the forked version.
- **Air-gapped deployments.** The local registry is the entire package universe; no external dependencies at install time.

### 5.3 Self-service variants

For deployments that don't want strict admin-gating, the local registry can support tiered access:

- **Approved packages** — vetted, available to all users.
- **Pending packages** — submitted but not yet vetted; available only to admins for testing.
- **Restricted packages** — vetted but require explicit per-user permission (e.g., production-data-touching graphs).

The package manager doesn't enforce these tiers itself; it integrates with the org's existing access control (LDAP, IAM, OIDC). Tier metadata is stored alongside packages in the registry.

### 5.4 The registry is just OCI

The local DAG registry is not a Rimsky-specific service. It's a standard OCI registry (Harbor, Artifactory, ECR, GHCR, distribution/distribution) hosting Rimsky packages alongside whatever else the org runs. The Rimsky package manager talks to it via standard OCI protocols.

This means:

- **No new infrastructure to deploy.** The org's existing registry is fine.
- **Inherited tooling.** Mirror, replication, signing, scanning all work as for any OCI artifact.
- **Federation is free.** Multiple registries, fallback ordering, all standard.

The only Rimsky-specific piece is the package format (the YAML manifest shape) and the install/registration tooling.

## 6. Versioning and dependency resolution

### 6.1 Hybrid versioning

Rimsky packages use a **hybrid versioning scheme**:

- **Semver tag** for human-facing version (`1.2.0`).
- **Content digest** for actual pinning (`sha256:abc...`).

The semver tag is what users type in dependency declarations:

```yaml
dependencies:
  executors:
    - type: llm-agent
      protocol: v1
      version: ">=2.0"
```

The digest is what gets resolved and locked at install time. A lockfile records the exact digest selected for each declared dependency, ensuring reproducible installs across deployments.

### 6.2 Capability-based matching

Graph dependencies are matched by **capability**, not by package name:

```yaml
dependencies:
  executors:
    - type: llm-agent       # required capability
      protocol: v1          # required protocol version
      version: ">=2.0"      # optional version constraint
```

The resolver's job is to find a registered executor (any package) that:

1. Provides the requested `type` and `protocol`.
2. Satisfies the `version` constraint if given.
3. Is in the deployment's allow-list (if the deployment uses one).

This decoupling means:

- Multiple executor implementations can satisfy the same graph (e.g., `claude-agent` and `gpt-agent` both providing `type: llm-agent`).
- Operators can swap implementations without touching graphs.
- Graphs don't need to track which specific executor packages exist.

### 6.3 Lockfile

After resolution, the package manager writes a lockfile per graph instance:

```yaml
graph_instance: abc-123
locked_at: 2026-04-26T12:00:00Z
resolved:
  package: standard-ingest-pipeline
  digest: sha256:graph-pkg-digest...
  dependencies:
    executors:
      - resolved_package: claude-agent
        digest: sha256:claude-digest...
        instance: claude-prod
    stores:
      - resolved_package: filesystem-bridge
        digest: sha256:fs-digest...
        instance: fs-shared
        substore_bound:
          logical_name: workspace
          physical_id: graph_abc123_workspace
          provisioned_at: 2026-04-26T12:00:01Z
```

The lockfile lives in the control-api's database alongside the graph instance. Re-installs against an existing instance reuse the lockfile (idempotent); operators can force a re-resolve to pick up new versions.

## 7. Trust and signing

OCI-native:

- **Sigstore / cosign** for package signing.
- **Provenance records** (SBOM, build attestations) carried as separate OCI artifacts.
- **Allow-list of trusted publishers** per deployment, configured in the control-api.

The package manager verifies signatures at install time. Failed verification fails the install (no override at the deployment level; the override is "add the publisher to the allow-list").

### 7.1 Inheritance of OCI trust

Since the package manager rides on OCI registries, all the existing trust tooling works:

- Image scanning (Trivy, Grype, etc.) on executor and store images.
- SBOM generation for dependency analysis.
- Cosign / sigstore signature verification.
- Cosign-signed transparency log entries.

Rimsky doesn't reinvent any of this. The Rimsky-specific piece is the package manifest signature, which is handled the same way as any OCI artifact.

### 7.2 Conformance attestation

In addition to signature, packages can carry **conformance attestations** — signed claims that the package passes a particular conformance suite at a particular version. The graph package's resolver can require:

```yaml
dependencies:
  executors:
    - type: llm-agent
      protocol: v1
      requires_conformance:
        suite: rimsky-executor-conformance
        version: ">=1.3"
        max_age_days: 90
```

A package whose conformance attestation is older than `max_age_days` fails dependency matching. This forces re-running conformance on stale packages before they can be used.

## 8. Install and registration flow

A worked example, end-to-end.

### 8.1 Phase 1: Platform provisioning (ops + IaC)

```hcl
# terraform / k8s / helm
resource "rimsky_deployment" "prod" {
  postgres_dsn = var.postgres_dsn
  # ...
}
```

Brings up scheduler, supervisor, control-api, postgres state DB. Standard IaC. No package manager involvement.

### 8.2 Phase 2: Executor and store deployment (ops + IaC)

Operator pulls package manifests for executors and stores they want to use:

```bash
$ rimsky-cli pkg fetch claude-agent@2.5.1
$ rimsky-cli pkg fetch postgres-multitenant-bridge@1.0.0
$ rimsky-cli pkg fetch filesystem-direct-bridge@0.9.2
```

This fetches manifests (and optionally the OCI image references) into the operator's working directory.

Operator deploys the images using their IaC, supplying values for each package's `registration.schema`:

```hcl
resource "kubernetes_deployment" "claude_agent" {
  # ... pulls ghcr.io/fallguy/claude-agent:2.5.1
  env {
    name = "ANTHROPIC_API_KEY"
    value_from = { secret_key_ref { name = "anthropic", key = "api_key" } }
  }
  env {
    name = "MAX_CONCURRENT"
    value = "10"
  }
}
```

Operator registers the deployed instances with Rimsky:

```bash
$ rimsky-cli register-executor \
    --package claude-agent@2.5.1 \
    --instance-name claude-prod \
    --endpoint claude-agent.workloads.svc.cluster.local:9090

$ rimsky-cli register-store \
    --package postgres-multitenant-bridge@1.0.0 \
    --instance-name pg-shared \
    --endpoint pg-bridge.workloads.svc.cluster.local:9091
```

The control-api validates the registration against the package's `registration.schema`, stores the binding, and exposes the executor/store to graphs.

### 8.3 Phase 3: Graph install (admin or self-service)

Admin (or user) installs a graph package:

```bash
$ rimsky-cli install standard-ingest-pipeline@1.2.0 \
    --instance-name daily-ingest \
    --params '{"region_root": "/data/raw"}'
```

The package manager:

1. Fetches the graph package and verifies its signature.
2. Resolves dependencies against registered executors and stores.
3. Validates capability matches; fails fast if any unsatisfied.
4. For each `substores` declaration, finds a multi-tenant bridge of the required kind, validates quota, calls `ProvisionSubstore`.
5. Binds logical names to the provisioned `SubstoreID`s.
6. Registers the graph spec with the control-api template registry, scoped to instance `daily-ingest`.
7. Writes the lockfile.

The graph is now installed and ready to instantiate.

### 8.4 Phase 4: Graph instantiation (user)

```bash
$ rimsky-cli instantiate daily-ingest --params '{"date": "2026-04-26"}'
```

Standard control-api instantiation, no package-manager involvement. The graph runs against its bound executors and provisioned sub-stores.

### 8.5 Phase 5: Uninstall (admin)

```bash
$ rimsky-cli uninstall daily-ingest
```

The package manager:

1. Confirms no active instances are running (or the operator forces).
2. For each sub-store with `auto_destroy: true`, calls `DestroySubstore`.
3. Removes the binding from the control-api.
4. Deletes the graph spec.
5. Deletes the lockfile.

Sub-stores with `auto_destroy: false` remain; the operator can manually destroy them via a separate admin endpoint.

## 9. What the package manager is not

To keep the IaC boundary visible, here's what the package manager **does not** do:

- **Provision real infrastructure.** No `terraform apply`, no `kubectl create`, no cloud API calls.
- **Manage substrate-level credentials.** Bridge admin creds, executor API keys, etc. are operator-config inputs to IaC; the package manager just forwards the schema.
- **Enforce runtime quotas.** Bridges enforce at runtime; the package manager only validates at install.
- **Run graphs.** The control-api runs graphs; the package manager just installs them.
- **Replace IaC.** The operator's existing IaC tooling continues to drive deployment and resource provisioning.
- **Compose sub-graphs.** Sub-graph composability is deliberately deferred; today a package contains exactly one graph spec.
- **Discover packages from random registries.** The deployment's allow-list is the source of trust; uncontrolled discovery is out of scope.
- **Manage tenant identity.** Multi-tenant Rimsky deployments use existing org identity (LDAP, OIDC); the package manager integrates with it but doesn't reinvent it.

## 10. Open questions

Items the conversation surfaced but did not settle. Each is worth its own focused discussion before implementation.

1. **Naming convention.** Reverse-DNS (`com.fallguy.executor-claude-agent`), scope+name (`@rimsky/executor-claude-agent`), or unqualified-with-namespacing-by-registry? Mechanical decision, propagates everywhere.

2. **Version range syntax.** Semver caret (`^2.0`), tilde (`~2.0.1`), explicit ranges (`>=2.0,<3.0`)? Most ecosystems converge on caret; Rimsky should too unless there's a reason not.

3. **Sub-graph composability.** Deferred. Should be designed as a local feature (graphs that import other graphs in the same deployment) before being layered onto packages.

4. **Federation between registries.** Public + private + fallback ordering. OCI handles most of this; the Rimsky-specific concern is allow-list semantics across registries.

5. **Conformance suite cadence.** How often must conformance be re-run? Who hosts the suite? Is it a per-publisher or per-deployment concern?

6. **Multi-tenant Rimsky deployments.** Per-tenant package namespacing, per-tenant allow-lists, per-tenant local registries. Probably layers on top of the design here but worth confirming.

7. **Migration semantics for graph upgrades.** Graph version N → N+1 with running instances. Schema migration, backfill, rollback. This is its own design space and probably needs a dedicated session.

8. **Inlined stores in the catalog.** The package format describes OCI-image bridges naturally. Inlined stores (Go modules compiled into Rimsky) need a slightly different shape — Go module reference instead of OCI image. Worth nailing down whether they share the same manifest or diverge.

9. **CLI vs declarative install.** §8 sketches a CLI (`rimsky-cli install`); a real deployment may want a declarative installer (Helm chart, Terraform provider) that can sit alongside other IaC. Both probably exist; the CLI is the canonical reference and the declarative wrappers ride on top.

## 11. Key decisions and rationale

A consolidated record of the substantive design decisions made during the conversation, with the alternatives that were considered. Captured so future sessions can read the rationale without re-litigating the choice.

### Package format

1. **Three-artifact catalog under one roof.** Graphs, executors, and stores share a unified manifest shape (capabilities, registration schema, conformance, signing). Considered separate registries per artifact type and Helm-chart-style bundled packages. Unified catalog: cross-references work, consistent vocabulary, single trust model, users discover all three from the same surface.

2. **Capability-first dependency matching, not package-name matching.** Graphs declare capabilities (type, protocol, version range); resolver finds satisfying packages. Considered name-based deps. Capability matching lets implementations swap without graph changes; multiple packages can satisfy the same need (e.g., `claude-agent` or `gpt-agent` both providing `type: llm-agent`).

3. **Hybrid versioning: semver tag + content digest.** Semver communicates intent in dependency declarations; digest guarantees reproducibility in the lockfile. Considered semver-only or digest-only. Hybrid combines human-readable expressivity with byte-exact pinning.

### Distribution

4. **OCI registries, not a Rimsky-native package server.** All three artifact types ride on standard OCI. Considered building a Rimsky-flavored registry. OCI gives free signing (cosign/sigstore), mirroring, replication, scanning, IAM, federation, regional caching, air-gapped story; the org's existing infrastructure handles it.

5. **Local DAG registry is just OCI.** A private OCI registry hosting Rimsky packages alongside whatever else the org runs. Considered a Rimsky-specific local-registry service. Chose standard OCI: no new infrastructure, inherited tooling, federation is free.

6. **Admin-curated library as the recommended pattern.** Local registry is the default source; public registries are optional secondary sources requiring explicit allow. Considered self-service-by-default with public as primary. Real ops won't allow uncontrolled package install; security, compliance, and governance work better at the curation layer.

### Boundary discipline

7. **Don't reinvent IaC.** Package manager stays strictly on the data side. Considered full-stack provisioning (deploy executors, allocate storage, manage networking). Reinventing k8s/Terraform/CloudFormation badly is worse than not trying; existing IaC tooling handles infrastructure better.

8. **Multi-tenant integration is logical sub-namespacing, not real provisioning.** Package manager calls bridge admin verbs to carve namespaces inside ops-provisioned substrates. Considered provisioning real resources. Logical carving is bounded and automatic; real provisioning is ops + IaC.

9. **Sub-graph composability deferred to a future session.** Considered including in v1 package design. Composing local graph fragments is a separate concern; should be designed as a local feature first, then layered onto packages once the local model is solid.

### Trust

10. **Conformance as signed attestations, not runtime checks.** Packages carry signed conformance attestations; resolver enforces freshness via `max_age_days`. Considered Rimsky running conformance at install. Attestations let resolver validate quickly without running tests; freshness constraint forces re-running on stale packages.

11. **Lockfile in control-api DB, not shipped with package.** Installations are deployment-specific; different deployments resolve to different concrete executor/store instances. Considered shipping a single lockfile with the package (Cargo.lock-style). A shipped lockfile would be wrong everywhere except where it was generated.

### Workflow

12. **Four-role lifecycle (platform ops / workload ops / graph author / instantiator).** Considered simpler one-or-two-role models. Real deployments have these distinctions even when smaller orgs collapse them; design must work at both ends; ignoring the tiering risks reinventing one-size-fits-all.

13. **Build-time install separate from runtime.** Install is a distinct phase that produces concrete templates registered with the control-api; runtime is the existing template execution. Considered runtime-only install per instance. Clean separation: package manager is install-time only; runtime doesn't depend on the package manager.

## 12. Picking up where we left off

### Settled

- **Three-artifact catalog** (graphs, executors, stores) with a unified manifest shape and consistent capability/registration declarations.
- **Data-vs-infrastructure boundary** is the load-bearing principle. The package manager owns the data side; ops + IaC owns the infrastructure side.
- **Local DAG registry** as the recommended deployment pattern; standard OCI registry, no Rimsky-specific service.
- **Multi-tenant store integration** at install time via bridge admin verbs (per `docs/2026-04-25-store-redesign.md` §15).
- **Hybrid versioning** (semver + digest), with capability-based dependency matching.
- **OCI-native trust** (sigstore/cosign) and conformance attestations as signed metadata.
- **Sub-graph composability deliberately deferred** to a separate session — must be designed as a local feature first.
- **Auth-blind** package manager (per `docs/2026-04-25-store-redesign.md` §14). Credentials flow as opaque payloads through the existing claim and attribute machinery; the package manager does not handle credential content.

### Watch out for

- **Don't cross the IaC boundary.** Provisioning sub-namespaces is fine; provisioning real infrastructure is not. If a feature seems to require the latter, back up — the answer is to provide an integration point for the operator's IaC, not to replicate it.
- **Resist Rimsky-specific registries.** OCI is the answer. Building a Rimsky-flavored package server reinvents wheels and adds operational burden.
- **Capability-first dependency matching.** Graph packages should declare capabilities and protocols, not specific implementation packages. This decoupling is what lets implementations be swapped without graph changes.
- **Lockfile is the source of truth at runtime.** Re-resolution must be an explicit operator action; deployments should not silently shift versions under graphs.
- **The recommended workflow is admin-curated.** The design supports self-service but the default operational model is gated review. Don't let UX optimizations push toward unreviewed self-service in places where security or compliance matter.
- **Don't conflate the package manager with the runtime.** The package manager is an install-time concern that produces concrete templates; the runtime runs the templates. The two should be separable; Rimsky's runtime should not require the package manager to be present.

### What's deliberately not in this doc

- **Implementation sequence.** Premature; depends on which Rimsky milestone the package manager is scoped to.
- **CLI command surface details.** Sketched in §8 but not specified; will be designed when implementation is ready.
- **Specific OCI registry recommendations.** Operators choose; Rimsky is registry-agnostic.
- **Multi-tenant Rimsky deployment semantics.** Layered concern; per §10 open question.
- **Public marketplace mechanics.** Discovery UX, ratings, monetization — out of scope for now; the design supports a public marketplace later but doesn't prescribe one.
- **Sub-graph composability.** Deferred; will be designed in a separate session as a local feature first, then layered onto packages.

This doc is the conversational state of the package manager design as of 2026-04-26. It captures the strategic decisions (three artifact types, IaC boundary, local registry workflow, multi-tenant integration) without prescribing implementation. Future sessions can pick up by addressing the open questions or starting on a specific implementation milestone.
