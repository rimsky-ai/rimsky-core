# Rimsky Development Kit (RDK)

**Date:** 2026-05-14
**Status:** sketch / forward-looking design
**Companion sketches:** `2026-05-14-data-platform-extensions.md` (data side),
`2026-05-14-agentic-platform.md` (agentic side). The RDK is the developer-
facing authoring layer over both — it's how Python developers build
templates that exercise the data-platform extensions and the agentic
patterns from a single project.

## What this covers

A Python authoring + packaging + deployment tool that lets a developer
write **one Python file** containing template spec + executor logic +
(optionally) claim-producer logic, and deploy the whole thing with one
command.

The RDK preserves rimsky's content-addressed declarative model — the
artifact deployed to control-api is canonical YAML — while closing the
authoring DX gap that pulls Python-native data engineers toward
Dagster / Airflow / Prefect.

The pattern: the RDK produces both a **canonical template artifact** and
one or more **executor containers**. Rimsky-core doesn't change; the
bundled `python-runtime` executor (also new) is what makes the user's
Python code addressable as a rimsky executor.

Three deliverables shape the work:

- A **Python RDK library** (`rimsky-rdk` on PyPI) with the builder API,
  decorator API, container packaging, and deploy CLI.
- A **bundled `python-runtime` executor** that hosts user-supplied Python
  handlers as a rimsky executor service.
- A **bundled `python-runtime` claim producer** for the more advanced use
  case of user-supplied custom producers.

Plus a small control-api extension for dynamic executor registration.

**Scope discipline:** Python-only first. The TypeScript / Go / Rust
equivalents follow the same pattern; we ship the Python implementation,
document the shape, invite community ports for other languages, or
generate them later. Most consumers who push hardest on this adoption
point are data engineers, and the data-engineering vernacular is Python.

---

# Part 1: the framing

## Why this works without compromising rimsky's model

The architectural concern with code-as-DAG systems (Airflow, Dagster) is
that they conflate the *authoring artifact* with the *runtime
implementation*. The code IS the DAG; the DAG runs by importing the code;
versioning is fragile; the orchestrator becomes a Python interpreter.

The RDK splits these concerns cleanly:

- **Authoring artifact**: the user's Python file. Versioned in the
  user's repo, like any code.
- **Canonical template**: produced by the RDK. JCS-canonicalized YAML;
  content-hashed; submitted to control-api. Same artifact shape today's
  YAML-authored templates produce.
- **Executor implementation**: the user's decorated handler functions,
  packaged into a container. The bundled `python-runtime` executor hosts
  them. Standard out-of-process executor; rimsky doesn't know it
  contains user code.

Rimsky's perspective:

- Templates are still canonical YAML; content-addressed; deployable.
- Executors are still out-of-process gRPC services; the python-runtime
  container is one such service, with user-supplied handlers baked in.
- Claim producers are still out-of-process services; same pattern.
- No code execution inside rimsky-control-api, rimsky-supervisor, or
  rimsky-scheduler.

The developer's perspective:

- One file. Spec authoring (`template.asset(...)`) and implementation
  (`@template.executor_node`) sit alongside each other.
- One command. `template.deploy(...)` builds the container, pushes it,
  registers the executor, submits the YAML.
- Standard Python tooling. `pytest` for local tests; `pip install` for
  dependencies; type hints for IDE support.

The user's pitch — *"this doesn't have to land that deeply inside of
rimsky"* — is exactly right. Rimsky-core changes are minimal: a new
bundled executor image (`python-runtime`); a new control-api endpoint for
dynamic executor registration. The rest is the RDK library.

## What rimsky-core gains

- `executors/python-runtime/` — bundled container image.
- `stores/python-runtime/` — bundled container image (later phase).
- `route:POST /executors` — new control-api endpoint for dynamic executor
  registration. Operator-scoped auth.

## What rimsky-core does NOT gain

- No Python interpreter in any rimsky binary.
- No source-code submission endpoint.
- No sandboxing logic.
- No code-as-DAG semantics in the canonical template format.
- No coupling of canonical-artifact identity to the authoring tool's
  version.

---

# Part 2: the Python RDK library

## Distribution

- PyPI package `rimsky-rdk`. Versioned alongside rimsky-core; published
  from CI on each rimsky tag.
- Submodules:
  - `rimsky_rdk.template` — `Template`, builder API, canonicalization.
  - `rimsky_rdk.executor` — decorators for inline executor handlers
    (`@template.executor_node` and friends).
  - `rimsky_rdk.claim` — decorators for custom claim producers (later
    phase).
  - `rimsky_rdk.deploy` — container build, registry push, executor
    registration, YAML submission.
  - `rimsky_rdk.cli` — `rimsky-rdk` CLI (`rimsky-rdk deploy`,
    `rimsky-rdk validate`, `rimsky-rdk run-locally`).

The CLI is a thin wrapper over `template.deploy(...)` and friends — useful
when developers want a CI-friendly invocation surface (`rimsky-rdk deploy
template.py --registry=...`) rather than calling `python template.py`.

## Builder API: declaring the template

The template is built up incrementally; the RDK validates progressively
against rimsky's canonical schema; at deploy time, the final object
canonicalizes to JCS YAML.

```python
from rimsky_rdk import Template, Table, Geo, Blob
from rimsky_rdk.types import Literal

template = Template(name="analytics-pipeline", version="1.0")

# Optional: declare params shape for instances
template.params_schema({
    "project_id": str,
    "category": Literal["alpha", "beta", "gamma"],
    "region": str,
})

# Declare an asset (durable blessed-typed attribute + its producer
# node, expanded by canonicalization into separate spec entries)
items = template.asset(
    name="items",
    type=Table,
    materialization="partition_overwrite",
    partitions={"kind": "time", "resolution": "daily"},
    lifetime="durable",
    schema={
        "columns": {
            "item_id": "string",
            "category": "string",
            "value": "double",
            "geometry": "geometry",
        }
    },
)

# Declare a node that produces the asset
template.node(
    name="ingest-items",
    executor="http-node",
    produces=[items],
    depends_on=[],
    userdata={
        "url": "http://ingest-service/items/{{params.project_id}}",
        "method": "POST",
        "timeout_ms": 30000,
    },
)
```

Builder methods cover every template construct documented in
`docs/concepts/` and exercised in
`2026-05-14-data-platform-extensions.md`:

- `template.params_schema(...)` — instance params shape.
- `template.asset(...)` — asset shorthand (creates blessed-typed
  attribute + producer node pair).
- `template.attribute(...)` — direct attribute declaration when the
  asset shorthand isn't right.
- `template.node(...)` — node declaration with executor, deps, stores,
  userdata, lifecycle handlers.
- `template.claim(...)` — claim declaration on a node (alternative to
  declaring inline within `template.node(...)`).
- `template.verifier(...)` — verifier-executor declaration; canonicalizes
  into a separate verifier node bound to the parent via dependencies +
  claim inheritance.
- `template.sensor(...)` — bundled-sensor declaration.
- `template.fan_out(...)` — fan-out wrapper on a node.

Each builder returns a typed Python object the user can reference
elsewhere in the template (e.g., `items` above is an `Asset` instance
that other nodes can `produces=[items]` or `depends_on=[items]`).

## Decorator API: inline executor logic

The decorator surface registers a Python function as the implementation
for a node, AND adds the corresponding node spec to the template
referencing the bundled `python-runtime` executor.

```python
@template.executor_node(
    name="clean-items",
    produces=[cleaned_items],
    depends_on=[items],
    fan_out={"over": "{{deps.items.partitions}}", "parallelism": 8,
             "aggregator": "map_partitioned"},
)
def clean_items(inputs, outputs, params, ctx):
    """Clean each partition's items in parallel."""
    df = inputs.items.to_polars()
    cleaned = df.filter(pl.col("value").is_not_null())
    outputs.cleaned_items.write_polars(cleaned)
```

Two things happen at decoration time:

1. The function is registered in the template's internal handler
   registry — keyed by node name.
2. A node entry is added to the template's spec:
   ```yaml
   - type: clean-items
     executor: python-runtime
     produces: [cleaned-items]
     dependencies: [items]
     fan_out: { over: ..., parallelism: 8, aggregator: map_partitioned }
     userdata:
       handler: clean_items   # name to dispatch internally to
   ```

The bundled `python-runtime` container, when dispatched for a node with
`userdata.handler: clean_items`, looks up the registered handler and
invokes it with resolved inputs / outputs / params / context.

## Function signature conventions

The decorated function takes named parameters following a documented
shape. The RDK uses Python type hints for IDE support and runtime
validation.

```python
from rimsky_rdk.executor import Reads, Writes, Context

@template.executor_node(...)
def my_handler(
    inputs: Reads[("upstream_a", Table), ("upstream_b", Geo)],
    outputs: Writes[("output_a", Table)],
    params: dict,                  # instance params + fan_out value
    ctx: Context,                  # claim handles, named-event emit, snooze, etc.
):
    ...
```

- `inputs` / `outputs` are typed containers built from the node's
  declared dependencies and produced assets. The RDK validates at
  decoration time that the function's `Reads[...]` annotation matches
  what the node declaration says.
- `params` is the merged map of instance params + any fan-out value.
- `ctx` exposes context-shaped capabilities: `ctx.emit(name, payload)`
  for named events; `ctx.snooze(resume_at=..., reason=...)` for park;
  `ctx.claim("alias")` for claim handles when needed.

These are conventions enforced by the bundled `python-runtime` executor,
not by rimsky-core.

## Decorator variants for other node shapes

`@template.executor_node` is for general work nodes. Specializations
exist for common shapes:

```python
# Sensor node — runs on schedule; emits events on observation
@template.sensor(
    name="watch-bucket",
    schedule={"cron": "*/5 * * * *"},
    invalidates=[my_target_node],
)
def watch_bucket(inputs, outputs, params, ctx):
    new_objects = list_new_objects_since(params.cursor)
    for obj in new_objects:
        ctx.emit("new_object", {"key": obj.key})
    outputs.cursor.write({"watermark": now()})

# Verifier node — runs against an upstream's writeback
@template.verifier(
    name="check-items",
    on=[items],
    severity="error",
)
def check_items(inputs, outputs, params, ctx):
    df = inputs.items.to_polars()
    if df.filter(pl.col("item_id").is_null()).height > 0:
        return ctx.fail("null item_ids found")
    return ctx.pass_()

# Agent node — wraps claude-agent (or other agent executors)
@template.agent_node(
    name="analyze",
    model="claude-opus-4-7",
    system_prompt="...",
    tools=["WebSearch", "WebFetch"],
    mcp_servers=[{
        "from_claim": "knowledge_store",
        "expose_tools": ["knowledge_search", "knowledge_write"],
    }],
)
def analyze(inputs, outputs, params, ctx):
    """Body unused; agent_node uses claude-agent executor, not python-runtime.
    Function exists for type-check + documentation purposes only."""
    pass
```

The `@template.agent_node` is interesting because **the implementation
isn't in the Python function** — the function body is documentation. The
executor for that node is `claude-agent` (or whatever agent executor),
which runs the agent loop independently. The RDK uses the decorator to
record the node spec; the function body never runs.

This is the right answer for agent nodes: the agent IS the implementation;
the Python function is the spec. Documentation in the docstring explains
that the body isn't executed.

For nodes whose implementation is genuinely in the user's Python code
(work nodes, sensors, verifiers, derive nodes), the function body IS the
implementation; the bundled `python-runtime` runs it. The two cases are
clearly distinguished by which decorator the user picks.

## Local validation

The RDK validates progressively as the template is built:

- Each builder call validates its arguments against the canonical schema.
- Decorator calls validate the function signature against the declared
  node shape (e.g., `inputs: Reads[...]` must match `depends_on=[...]`).
- `template.validate()` runs the full canonical-schema validation against
  the in-memory template. Optional explicit call before deploy.
- `template.canonicalize()` produces the JCS-canonicalized spec bytes
  and computes the content hash.

Errors are surfaced as Python exceptions with clear messages. The
developer's IDE / terminal sees them locally; no network roundtrip.

## Dry-run via control-api

For producer-side validation (the `ValidateClaimantUserdata` extension
from the agentic-platform sketch), the RDK supports dry-run submission:

```python
template.dry_run(control_api="http://localhost:8080")
```

The control-api runs full template-registration validation — including
each producer's `ValidateClaimantUserdata` if implemented — without
persisting. Returns the canonical hash + structured errors. Surfaces
producer-side issues at authoring time.

Probably the default behavior for `template.deploy()` is "dry-run first;
only proceed if dry-run passes." Operators can opt out for tight
authoring loops.

## Deploy flow

The `template.deploy(...)` call orchestrates everything. The full flow:

```python
template.deploy(
    control_api="http://localhost:8080",
    container_registry="docker.io/myorg",
    auth={
        "control_api": {"bearer_token": "..."},
        "registry": {"username": "...", "password": "..."},
    },
    image_tag="v1.0-{template_hash}",
)
```

Steps the RDK executes:

1. **Canonicalize the template** to JCS YAML; compute content hash.
2. **Dry-run validation** against the control-api (catches schema /
   producer-validation errors before container build).
3. **Build the executor container** — extract the registered handler
   functions and the user's Python package; produce a Docker image
   layered on top of the `python-runtime` base image. Tag with the
   computed image tag (default includes template hash for
   reproducibility).
4. **Push the image** to the configured registry.
5. **Register the executor** with the control-api via the new
   `route:POST /executors` endpoint. The registration entry maps an
   executor name (derived from the template, e.g.
   `python-runtime-analytics-pipeline-{hash}`) to the pushed image.
6. **Submit the template** to the control-api via `route:POST /templates`.
7. **Deploy the template** via `route:POST /templates/{hash}/deploy`.

Each step is observable; failures at any step surface back to the
developer with structured errors. The RDK can be invoked with
`--steps=build,push` to halt before deploy for CI-shaped flows that want
to gate deployment on additional review.

---

# Part 3: bundled `python-runtime` executor

The container image that makes RDK-authored templates runnable. Generic;
hosts user-supplied handlers; implements the rimsky executor protocol.

## Image layout

The base `python-runtime` image, built and published by rimsky:

```
/opt/rimsky/python-runtime/
  ├── entrypoint.py            # boots the gRPC server
  ├── runtime.py               # implements proto:executor.proto::Executor
  ├── dispatch.py              # node-type → handler routing
  ├── adapters/                # substrate adapters (table → polars, etc.)
  └── requirements.txt         # rimsky-side deps (grpcio, pyarrow, etc.)
```

The user-built image layers their code on top:

```
FROM rimsky/python-runtime:VERSION

# User's deps
COPY pyproject.toml ./
RUN pip install -e .

# User's code (includes the @template.executor_node functions)
COPY my_pipeline.py ./

# Tell the runtime where to find user code
ENV RIMSKY_USER_PACKAGE=my_pipeline
```

At startup, the runtime imports `my_pipeline`; the import side-effects
register the decorated handlers; the runtime exposes them via the
executor protocol.

## Handler registration

The runtime maintains an in-process registry of handler functions, keyed
by the name declared in the `@template.executor_node(name="...")`
decorator. The decorator's side effect at import time is to add an entry
to this registry.

When a dispatch arrives:

1. Runtime extracts `userdata.handler` from `ExecuteRequest.userdata`
   (the name set by the RDK at template-build time).
2. Looks up the handler in the registry.
3. Resolves inputs / outputs from the substituted attributes and claim
   addresses.
4. Invokes the handler with the typed parameters.
5. Captures the handler's writes into the outputs container.
6. Returns a `Complete` (or `Error` if the handler raised).

For named events: the handler's `ctx.emit(name, payload)` translates to
a `NamedEvent` protocol message before the terminal.

For park: `ctx.snooze(resume_at=..., reason=...)` translates to the
`Snooze` terminal event.

## Substrate adapters

The runtime ships built-in adapters for the blessed types (blob, table,
geo) once those land in the data-platform-extensions work. Each adapter
resolves a claim address to a Python-native interface:

- `Table` claim address → `output.write_polars(df)`, `input.to_polars()`,
  `input.iter_rows()`.
- `Geo` claim address → `output.write_geopandas(gdf)`,
  `input.to_geopandas()`.
- `Blob` claim address → `output.write_bytes(b)`, `input.read_bytes()`.

These match the per-language executor SDK adapters from the data-
platform sketch — the python-runtime container IS that SDK in its
hosted form.

For substrates beyond the blessed types (consumer-built claim
producers), the runtime exposes the raw claim handle via `ctx.claim(alias)`;
the user's code talks to the substrate directly using its native library.

## Image versioning

The base image (`rimsky/python-runtime:VERSION`) is versioned with
rimsky-core. Each rimsky release publishes a matching python-runtime
image.

User-built images are tagged per-template-version, typically including
the template's content hash for reproducibility:

```
docker.io/myorg/analytics-pipeline:1.0-sha256-abc123...
```

When a developer rebuilds and redeploys with the same content hash, the
image tag is identical — the build is reproducible (assuming
dependency lock files). When they change the template or handler code,
a new content hash produces a new tag.

## Concurrency

The runtime hosts handlers in-process. Concurrent dispatches share the
Python interpreter (with GIL constraints). For CPU-bound workloads, the
runtime can be configured with a worker pool (multiprocessing); for
I/O-bound or async workloads, single-process asyncio is sufficient.

Configuration via RDK at template build time:

```python
template.runtime_config(
    concurrency_mode="async",       # or "threaded" or "multiprocess"
    max_concurrent_dispatches=8,
)
```

This serializes into the runtime's startup config, baked into the
container.

## Health, observability

The runtime exposes:

- gRPC health-check endpoint (standard).
- Prometheus-shaped metrics endpoint on a configurable port.
- Structured logs to stdout (JSON; collected by the operator's log
  pipeline).

These are runtime concerns; not RDK-author concerns. Operators see
metrics like any other bundled executor.

---

# Part 4: control-api executor registration endpoint

The piece that makes dynamic executor registration possible. New
control-api surface.

## The endpoint

```
POST /executors
{
  "name": "python-runtime-analytics-pipeline-sha256-abc",
  "image": "docker.io/myorg/analytics-pipeline:1.0-sha256-abc",
  "transport": "grpc",
  "endpoint_template": "http://{service-name}:9090",  # how to dial it
  "deployed_by": "<credential identity>",
  "metadata": {
    "template_hash": "sha256-abc...",
    "rdk_version": "1.0.0",
  }
}
```

The control-api validates:

- Caller is authorized to register executors (scoped credential).
- The image is reachable from the cluster's runtime (operator-policy
  check; may require the image to be in a pre-approved registry).
- The endpoint template resolves to a routable address.

On success, the executor entry is added to the cluster's executor
registry. Subsequent dispatches to templates referencing the executor by
name route to the new image.

## Operator-developer handoff

The new endpoint blurs the operator-developer split. Three deployment
postures the operator can choose:

**Posture A: developers can register their own executors.** Operators
issue credentials scoped to `POST /executors` (possibly with namespace
restrictions, e.g. only register executors prefixed `team-x-*`). The
RDK uses these credentials. Workflow: developer runs
`template.deploy(...)`; everything happens transparently.

**Posture B: GitOps-driven executor registration.** Operators don't grant
developers `POST /executors` permission. The RDK produces an "executor
registration patch" file (YAML or JSON) the developer commits to the
operator's config repo. Operator's GitOps pipeline applies the patch
(creates the executor entry via control-api with operator credentials).
Workflow: developer runs `template.deploy(--no-register-executor)`; gets
the patch; commits; operator's CI deploys.

**Posture C: managed registry.** Operators configure rimsky to
auto-discover executors from a configured container registry path
(`docker.io/myorg/rimsky-executors/*`). Developers push images to the
path; rimsky polls or webhook-listens; auto-creates executor entries.
Workflow: developer runs `template.deploy(--no-register-executor)`; image
push is sufficient. More speculative; ship Posture A + B first.

The RDK supports all three postures via flags on `template.deploy()` and
operator-side configuration. v1 ships A and B; C is a future extension.

## Auth and audit

- The `POST /executors` endpoint requires authentication, same surface as
  other write-shaped control-api endpoints.
- Per-credential scoping: which executor name patterns the credential
  can register.
- Audit log: every executor registration records the credential,
  timestamp, image, template hash, metadata.

This is real new write surface on the control-api; treat it with the same
care as `POST /templates`.

## Lifecycle

Executor registrations are persistent. When a developer redeploys a
template, the RDK can either:

- **Replace** the existing executor entry (same name; new image).
- **Add a new entry** with a different name (allows side-by-side
  versions).

Default: replace, with the prior image's tag retained in the executor
entry's history field for rollback. Operator can configure this.

Garbage collection: when no template references an executor entry, the
entry is eligible for removal. The RDK's deploy flow can also delete its
prior entries on each redeploy if configured to.

---

# Part 5: local development and testing

A core part of the developer's experience — running templates locally
before pushing to a cluster.

## In-process local run

For tight authoring loops, the RDK runs the python-runtime in-process:

```python
# In a test or REPL
result = template.run_locally(
    instance_key="test-instance",
    params={"project_id": "alpha", "category": "alpha", "region": "us"},
    seed_attributes={
        "items": [{"item_id": "x1", "value": 1.0, ...}, ...],
    },
)

print(result.assets.cleaned_items.to_polars())
print(result.events)
```

Behind the scenes:

- The RDK loads the template's handler registry.
- A local test harness simulates the rimsky scheduler/supervisor enough
  to dispatch nodes in dependency order.
- Each handler runs in-process; inputs and outputs use in-memory
  substrate adapters (no real Parquet writes, no S3 calls).
- The harness collects results into a `LocalRunResult` for inspection.

Suitable for unit-testing handler logic. Not suitable for testing
rimsky-specific behavior (claims, lifecycle handlers, frame resolution)
— for that, use a real rimsky cluster.

## Docker-compose-based local cluster

For end-to-end testing, the RDK ships a `rimsky-rdk dev` command that
boots a local docker-compose rimsky cluster:

```sh
rimsky-rdk dev               # boots cluster, watches my_pipeline.py
                             # for changes, rebuilds + redeploys on save
```

This composes with rimsky's existing `deploy/docker-compose.yml`
reference deployment. The dev command:

- Brings up the compose stack (Postgres, scheduler, supervisor,
  control-api, bundled executors).
- Builds the python-runtime image for the local template.
- Registers the executor + deploys the template against the local
  cluster.
- Watches for file changes; rebuilds + redeploys on save.

Tight authoring loop without leaving the IDE.

## Test fixtures

For pytest-style testing:

```python
from rimsky_rdk.testing import RimskyFixture

def test_clean_items_filters_nulls(rimsky_fixture: RimskyFixture):
    result = rimsky_fixture.dispatch(
        node="clean-items",
        inputs={"items": [{"item_id": "a", "value": 1.0},
                          {"item_id": "b", "value": None}]},
    )
    assert len(result.outputs.cleaned_items) == 1
```

The `RimskyFixture` is a pytest fixture that creates an isolated in-
process runtime; each test dispatches a node with synthetic inputs and
asserts on outputs. Standard testing pattern; familiar to Python
developers.

---

# Part 6: container build details

The packaging layer. Where most of the platform-engineering work lives.

## Build context

The RDK assembles a build context from:

- The user's Python package (the directory containing the file with
  `@template.executor_node` decorators, plus any imported modules).
- The user's dependency declaration (`requirements.txt`, `pyproject.toml`,
  or `setup.py` — RDK auto-detects).
- Any lock files (`requirements.lock`, `poetry.lock`, `uv.lock`).
- The bundled `python-runtime` base-image reference.

The RDK generates a Dockerfile dynamically (or uses BuildKit's
`build context` API to avoid filesystem materialization). Build runs via
the locally available builder (Docker / Buildah / Podman / nerdctl).

## Dependency capture

Python's dependency story is operationally tricky. The RDK supports:

- **`requirements.txt`** — simplest; the build pins versions from the
  file.
- **`pyproject.toml`** with Poetry / uv / pip — the RDK detects which
  tool, uses appropriate lock file, builds with reproducibility.
- **Conda environments** — supported via `environment.yml`; uses
  micromamba in the build.

For determinism: lock files are required by default. Builds without lock
files emit a warning unless `--allow-unlocked` is passed.

## Multi-stage builds

The generated Dockerfile uses multi-stage builds for size optimization:

- Stage 1: install build-time deps (pip, build essentials).
- Stage 2: copy dependency files; install deps into a virtual env.
- Stage 3: copy user code; final image is base + venv + user code.

Standard Python container patterns; nothing rimsky-specific. The RDK
optimizes for build cache reuse — dependency layers cache separately
from code layers.

## Native extensions

Some Python libraries (numpy, scipy, pyarrow, polars, geopandas) bundle
native code. The base `python-runtime` image is built with the common
libraries pre-installed to avoid duplicating native dep installation in
every user image. User code that adds further native deps does so on top.

## Image scanning

Optional integration with image-scanning tools (Trivy, Snyk, etc.) at
build time. The RDK can be configured to fail builds on CVE-detected
vulnerabilities above a threshold. Operator policy; not v1 default.

## Multi-architecture

Bundled `python-runtime` images are published for `linux/amd64` and
`linux/arm64`. The RDK supports building multi-arch images via
`docker buildx`.

---

# Part 7: claim producer packaging

A secondary capability for advanced consumers who need custom claim
producers. Same packaging machinery; different protocol.

## The bundled `python-runtime` claim producer

`stores/python-runtime/` is the symmetric image for claim producers: a
generic container that hosts user-supplied Python claim-producer
implementations.

## Decorator surface

```python
from rimsky_rdk.claim import ClaimProducer

@template.claim_producer(name="project-alpha-knowledge")
class KnowledgeProducer(ClaimProducer):
    def open(self, scope, intent, claim_id):
        # Resolve scope to a substrate-native handle
        path = self._resolve_path(scope)
        return self.OpenResponse(
            address=path,
            payload={...},
            realized_write_semantics="staged_async",
        )

    def commit(self, claim_id):
        # Promote staging to canonical
        ...

    def abandon(self, claim_id):
        # Drop staging
        ...

    def release(self, claim_id):
        ...

    def capabilities(self):
        return self.Capabilities(
            protocols=["claim_producer"],
            write_semantics_allowed=["staged_async"],
            scope_conflict_matrix=...,
        )

    # Optional: producer-side userdata validation
    def validate_claimant_userdata(self, userdata, bindings):
        # parse userdata; return errors if any
        ...
```

The `@template.claim_producer` decorator:

1. Registers the class in the template's claim-producer registry.
2. Adds a claim-producer reference to the template's spec (referencing
   the bundled `python-runtime` claim-producer image with this user
   class baked in).

At deploy time, the RDK builds a derived image from `stores/python-
runtime/` with the user's class included, pushes it, registers it as a
claim producer with the cluster.

## Use cases

Custom claim producers are niche. The 80% case is "use a bundled
producer." But for consumers who need substrate-specific semantics rimsky
doesn't bless — a custom tabular store, a domain-specific staging
pattern, a queue-shaped substrate not covered by `stores/postgres` —
this lets them write the producer in Python and deploy via the same
flow.

Most templates won't use this. The capability exists for completeness.

## Lifecycle and registration

Symmetric with executor registration: control-api gains
`route:POST /claim-producers` for dynamic claim-producer registration.
Same auth scoping, same audit, same operator-developer-handoff postures
(A: developer-direct; B: GitOps; C: managed registry).

---

# Part 8: project-level concerns

## Multi-template projects

A real codebase often has many templates. The RDK supports:

```python
from rimsky_rdk import Project

project = Project(name="analytics-platform")

template_ingest = project.template(name="ingest")
template_ingest.asset(...)
...

template_process = project.template(name="process")
template_process.asset(...)
...

project.deploy(control_api=..., registry=...)
```

The `Project` object groups templates; `project.deploy()` deploys all
of them. Composes with rimsky's existing `compose:` tag convention:
project-prefixed tags (`compose:analytics-platform:ingest`,
`compose:analytics-platform:process`) are automatically applied.

## Shared handler code

When multiple templates share handler logic, the developer organizes
their code in Python modules; templates import handlers:

```python
# common/cleaners.py
from rimsky_rdk.executor import handler

@handler
def clean_polars_dataframe(df, schema):
    """Reusable cleaning logic."""
    ...

# templates/ingest.py
from common.cleaners import clean_polars_dataframe

template = Template(name="ingest", ...)

@template.executor_node(...)
def clean_items(inputs, outputs, params, ctx):
    df = inputs.items.to_polars()
    cleaned = clean_polars_dataframe(df, schema=...)
    outputs.cleaned.write_polars(cleaned)
```

Standard Python composition. The RDK packages whatever's imported into
the container.

## CI integration

The RDK is designed to run in CI:

- `rimsky-rdk validate template.py` — schema validation + producer dry-
  run; fails build on errors.
- `rimsky-rdk build template.py --output=image.tar` — produce the
  image without pushing (for review).
- `rimsky-rdk deploy template.py` — full deploy.

Standard CI pipeline shape:

```yaml
# .github/workflows/deploy.yml
- name: Validate
  run: rimsky-rdk validate template.py
- name: Build and push
  run: rimsky-rdk build template.py --push
- name: Deploy
  run: rimsky-rdk deploy template.py --no-build  # already pushed
```

## Versioning and rollback

Template versions in the RDK:

```python
template = Template(name="ingest", version="1.0")
```

The `version` field is for human-readable display; rimsky's content
hash is the authoritative version. The RDK applies the version as a
tag (`compose:analytics:ingest@1.0`) when deploying.

For rollback: previous versions of an executor image are retained in the
registry; the RDK supports `rimsky-rdk rollback ingest --to-version=0.9`
which re-points the executor entry to the old image without rebuilding.

---

# Part 9: deferred (multi-language ports)

Python is the v1 ship. The same pattern extends to other languages; we
document the shape and invite community contributions (or generate them
later when ergonomics matter).

## TypeScript

Same shape as Python:

- `@rimsky/rdk` on npm.
- Bundled `executors/typescript-runtime/` image.
- Decorator-equivalent (likely class-based or builder-based, since TS
  doesn't have Python's decorator-as-side-effect semantics).
- Container packaging via Docker.

The bundled `executors/claude-agent/` is already TypeScript — its
existence makes the TS path concrete (the agent executor IS the closest
analog to a hosted-user-code TypeScript executor today). When the TS
RDK ships, `claude-agent` may share machinery with `typescript-runtime`.

## Go

Compile-and-ship variant:

- `github.com/fallguyconsulting/rimsky-rdk-go` Go module.
- No bundled "runtime" image needed — Go binaries are static. The RDK
  compiles a binary with user-supplied handlers linked in; ships in a
  thin image.
- Builder API matches Python's shape, idiomatic for Go.

For services that already use Go: the RDK is a library; the build is a
standard `go build`; the resulting binary implements the executor
protocol.

## Rust / Java / .NET / others

Same pattern. Each language gets its own RDK + (interpreted languages
need) bundled runtime image. Community contributions welcome; we
maintain the Python implementation and a reference for what the surface
should look like across languages.

The canonical YAML template format is the language-neutral
interchange. As long as a language's RDK can produce the canonical
artifact and build a container implementing the executor protocol, it
plugs in.

---

# Part 10: phasing

The work stages roughly:

**Stage 1 — design lockdown.**

- Python RDK API surface (builder + decorator) finalized.
- Function-signature conventions for the python-runtime.
- Container build pipeline design.
- `route:POST /executors` endpoint design + auth model.
- Operator-developer handoff postures.

**Stage 2 — `python-runtime` executor first ship.**

- Bundled `executors/python-runtime/` container image.
- gRPC server implementation; handler registry; dispatch routing.
- Substrate adapters (initially limited to today's attribute types;
  expand with the data-platform extensions).
- Local-run test harness.
- Multi-arch image publishing.

**Stage 3 — Python RDK first ship (template authoring).**

- `rimsky-rdk` PyPI package.
- `Template`, builder methods, decorator surface.
- Local validation against rimsky's canonical schema.
- Dry-run against control-api.
- CLI surface (`rimsky-rdk validate`, `run-locally`).
- Distributed via PyPI; versioned alongside rimsky-core.

**Stage 4 — control-api executor registration.**

- `route:POST /executors` endpoint.
- Auth scoping and audit.
- Posture A (developer-direct) and Posture B (GitOps patch) supported.

**Stage 5 — RDK deploy flow.**

- Container build (Docker / Buildah / Podman).
- Registry push with auth.
- Executor registration via control-api.
- Template submission + deployment.
- End-to-end `template.deploy()` working.
- Documentation: tutorials for the single-file workflow.

**Stage 6 — dev experience polish.**

- `rimsky-rdk dev` command (docker-compose-based local cluster with
  hot-reload).
- Pytest fixtures.
- IDE-supportive type hints across the surface.

**Stage 7 — claim-producer packaging (secondary).**

- Bundled `stores/python-runtime/` claim-producer image.
- RDK decorator surface for `@template.claim_producer`.
- `route:POST /claim-producers` endpoint.
- Worked example.

**Stage 8 — composes-with-existing-platform-work.**

- RDK builder support for the data-platform extensions (partitions,
  materialization, fan-out, verifier decorators, sensors).
- RDK builder support for the agentic-platform additions (MCP-tool
  bindings, knowledge-store claims, supervisor templates).
- These naturally fall out as the platform extensions land; RDK is the
  authoring layer for them.

**Stage 9 — managed-registry posture (Posture C).**

- Auto-discovery of executors from a configured container registry.
- Speculative; ship if real consumer demand emerges.

**Deferred:**

- TypeScript RDK + `executors/typescript-runtime/`.
- Go RDK.
- Other-language ports.
- Cross-RDK shared canonicalization library (currently each RDK
  reimplements; future consolidation if drift becomes an issue).

---

# Open design questions

1. **Function signature standardization across decorator variants.**
   `@template.executor_node`, `@template.sensor`, `@template.verifier`,
   `@template.agent_node` — each has slightly different parameter shape.
   The RDK should document these uniformly so type checkers (mypy,
   pyright) catch mismatches in IDE.
2. **Implicit vs explicit handler registration.** The current design has
   the decorator side-effect-register at module import time. Alternative:
   explicit `template.register_handler(clean_items)` after the function
   definition. Decorator is more Pythonic; explicit is more transparent.
   Stick with decorator.
3. **Hot reload semantics.** `rimsky-rdk dev` watches files and rebuilds.
   What scope of change requires full container rebuild vs in-process
   handler swap? Probably: any Python file change triggers a hot swap
   for in-process dev; dependency changes (requirements.txt /
   pyproject.toml) trigger a full rebuild.
4. **Image registry pre-flight.** Before pushing, the RDK should verify
   it has registry credentials and that the namespace exists. Failing
   late (after a long build) is bad UX.
5. **Concurrent deployments.** Two developers running
   `template.deploy()` against the same template hash at the same time
   — what happens? Idempotent re-registration (template-registration is
   already idempotent on content hash). Executor entry registration:
   last-write-wins; both deploys produce the same image (same content);
   same image tag; same registry push (no-op for duplicates).
6. **Cache durability for build context.** BuildKit cache mounts, pip
   cache, etc. — the RDK should preserve caches across invocations for
   fast iterative builds.
7. **Image scanning and security policy.** Optional but worth design.
   Operators may want to enforce "no images from unapproved registries"
   or "no images failing CVE scan." The control-api's
   `route:POST /executors` endpoint can validate the image against
   operator policy at registration time.
8. **Per-credential namespace restrictions.** A developer credential
   should be able to register executors with names like
   `team-x-*` but not `team-y-*`. Standard ACL; needs design.
9. **Audit and provenance.** Every executor registration records who
   deployed, when, from which template hash, via which RDK version.
   Auditable; queryable via the control-api.
10. **The split between RDK and executor SDK.** The python-runtime
    container IS the per-language executor SDK's hosted form. Service-
    shaped executor authors (using just `rimsky_executor.executor`
    decorator without the RDK's container packaging) and template-shaped
    authors (using the full RDK) share decorator surface. Need to
    document the two paths so users pick the right one.
11. **Behavior under base-image upgrades.** When `rimsky/python-runtime`
    is bumped (security patch, protocol change), user-built images
    pinned to the old base lag. RDK should warn on outdated base; CI
    can be configured to fail. Operators may want to pin base versions
    explicitly.
12. **Cost / size of the python-runtime base image.** Pre-installed
    libraries (numpy, polars, pyarrow, geopandas, requests, …) add MB.
    Trade-off between fast cold starts and image size. Probably ship a
    "minimal" and a "fat" base; default to fat for convenience.
13. **Telemetry from user code.** Handler functions can emit named
    events via `ctx.emit`. Should the runtime also auto-emit telemetry
    (execution time, memory, errors) without the user opting in?
    Probably yes for operational observability; configurable per
    handler if needed.
