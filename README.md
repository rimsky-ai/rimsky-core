# Rimsky

Rimsky is a reactive node-graph orchestration platform for agentic
workloads. Work is declared as a graph of nodes; when a node's value
changes, the cascade marks dependents stale and the scheduler dispatches
them. The platform is domain-agnostic — it owns control flow,
concurrency, and persistence, and leaves the work itself to
out-of-process services that the consumer brings.

## 1. What rimsky is

Rimsky models a workload as a graph of typed nodes connected by
subscriptions. A node's executor — anything reachable over the executor
gRPC protocol — runs the work; node output ripples downstream through
the **cascade**, which decides which dependents become stale and
recompute. Concurrency across shared state goes through **claims**
acquired against named scopes via the claim-producer protocol. Templates
are content-addressed specifications; instances bind a template to
runtime parameters. A frame is one cascade resolution. Held subgraphs
let multiple nodes share an acquired claim and resolve it atomically at
subgraph completion.

The platform is pre-v1. Wire protocols, YAML config shapes, and
persistence schemas may change between versions; the safety properties
(deterministic-sorted-order multi-lock acquisition, verify-before-run,
claimant-guarded release, unified terminal-decision, auto-terminal
aggregate-outcome) are stable. Three runtime processes — scheduler,
supervisor, and control-api — communicate only through Postgres. The
control-api hosts both the operator HTTP+JSON surface and a coextensive
MCP skin so an LLM-driven operator can drive the platform on the same
verbs a human would.

This README is for evaluators deciding whether to engage with rimsky for
an agentic workflow problem. It frames what rimsky is, what it was
built for, what makes it different from the things you might be
pattern-matching against, and where to go next if the answer is yes.
Builders point their coding agent at this repo and let it walk them
through depth.

## 2. What rimsky was built for

Rimsky is an agent orchestrator that can also implement data
processing patterns — not a data orchestrator that happens to handle
agents. The primitives look superficially like a data-engineering
toolkit (assets, partitions, lineage, backfills, typed attributes), but
that surface exists to give agentic work durable handles, not because
data transformation is the headline. The patterns below are the ones
rimsky was designed against. If your problem looks like one of them,
keep reading; if it doesn't, an adjacent system is probably a better
fit.

**Watching external state and reacting.** Sensors observe external
systems — S3 prefixes, HTTP endpoints, cron schedules, inbound webhooks
— and send messages into the graph. The cascade fires downstream nodes
whose subscriptions match. The reactive logic lives in the graph
itself, not in code the consumer writes around a workflow engine. Where
a workflow engine asks you to write the reactive layer as orchestration
code, rimsky asks you to declare the subscription and trusts the
cascade to do the routing.

**Stateful agentic workloads at platform scale.** An LLM agent is an
executor — a service that takes inputs and produces outputs and named
events along the way. Output triggers downstream nodes through
subscriptions; failures park for human review; held claims gate access
to shared state; cascade routes the next agent invocation when a result
changes. Rimsky operates above the single-agent layer: it is not an
agent framework that orchestrates one agent's tool calls, it is the
platform that coordinates many agents and templates against shared
infrastructure.

**Subgraphs that succeed or fail atomically.** Held claims combined
with held subgraphs let an agent (or a chain of agents and
deterministic nodes) do N steps and either all commit or all roll back.
The producer's commit/abandon verbs run exactly once at subgraph
completion, determined by the aggregate outcome of every node that
co-held the claim. This is the pattern an agent needs when it has to
touch multiple systems coherently — a DAG scheduler where every step's
effects are independently persistent will leave half-applied state
behind on failure; rimsky's atomic-staging primitive makes the
all-or-nothing the default mode.

**Coordinating across shared state.** Claim producers expose a uniform
acquisition interface against arbitrary backing systems — a filesystem,
a Postgres table, a vector store, a custom service. Named locks gate
deployment-wide capacity. The platform compares scope bytes through the
producer's conflict matrix; it doesn't know what "row 42" means or care
whether two scopes overlap semantically — the producer answers that.
Where a workflow engine forces you into its state model, rimsky lets
you keep your state where it is and gate access to it through a
producer that understands the domain.

**Data operations as a service to agentic work.** When agents produce,
transform, or materialize data, rimsky's typed-attribute system (blob
and table blessed today, geo on the way), partitions, fan-out, and
lineage are there to support that work. They are plumbing for the
agent's actual job — letting an agent return a `table` and have its
shape, location, and provenance tracked without the agent writing
data-platform code. They are not the headline. A data-engineering
platform with an opinion about transformation languages, a semantic
layer, or a metric definition surface is solving a different problem.

The thread across all five: rimsky was built when an agentic workflow
needed platform-grade orchestration and the available platforms were
either too task-shaped (single-DAG schedulers, no shared-state
coordination), too data-shaped (transformation-first platforms with
opinions about how data moves), or too agent-shaped (single-agent
frameworks with no coordination layer) to fit.

## 3. Design philosophy

Rimsky's primitives only make sense alongside the principles that
constrain them. The core split is that graphs are control flow and
executors are domain logic. The platform decides which nodes are
eligible, which dependents go stale, which claims conflict, when a
frame resolves. The platform does not know what the work is for, what
the bytes mean, what the agent is reasoning about, or what the table
contains. Every domain-shaped piece of a deployment — the templates,
the userdata, the MCP catalogs the agents access, the audit-trail
content — lives in the consumer's repository, not in rimsky's.

The platform treats its carrier byte streams as inert: userdata, claim
scope, claim address, claim payload, attribute values, scratch,
executor error payloads, and message payloads.
Rimsky does not log them, normalize them, index them, validate them
beyond the schema gates, attach them to traces, or include them in
error messages. Inertness is not minimalism; it is what keeps rimsky
out of the consumer's domain. Once the platform reads bytes for
meaning, it becomes a partial participant in the domain and inherits
bug surface and security surface that belongs to the consumer. Two
properties lock this in source — userdata opaque and claim content
inert — and removing them would unwind the design.

No domain helpers ship. Reference service implementations are
illustrative — they cover the protocol's shape and the deployment
story, not a curated catalog of production-ready domain pieces. A platform that ships domain helpers becomes a platform
whose users file requests for more domain helpers; rimsky does not grow
features to solve the consumer's problem, it provides primitives that
the consumer composes against their own services.

Deterministic transformations belong inside executors, not inside
rimsky. A node with no executor declared is a native node — the cascade
synthesizes a completion once its claims are acquired, and the value
carried downstream is whatever the upstream nodes wrote. Non-trivial
transformations run through executors like any other work. Patterns
that look like they would benefit from a special node type — agent
self-blocks where an agent emits a structured failure and a downstream
node routes on the failure class, confidence-driven branching where a
CEL `when:` predicate fires only the matching subscriber — are all
expressed
through the existing executor and subscription surfaces. Rimsky has no
special-cased "deterministic node" type because the cascade,
substitution, retry policy, and claim semantics are already correct for
pure code.

Pre-v1 still holds its safety properties because they are load-bearing
for any consumer. The release stance gives rimsky permission to break
wire shapes and schema between versions; it does not give the platform
permission to weaken acquisition determinism, the verify-before-run
guard, the claimant-guarded release, or the unified terminal-decision
engine. Pre-v1 is about iteration speed on surfaces; the safety
properties are stable.

## 4. Load-bearing primitives

The primitives below are what an evaluator needs to know exists. Each
gets one paragraph here; the formal definitions and boundaries live in
the design catalog.

**Templates and instances.** A template is a content-addressed
specification of a graph — keyed by a hash over its canonicalized
bytes, so two equal templates compare equal and no template can be
silently mutated. An instance binds a template to runtime parameters
and carries the live execution state. Movable string tags point at
template hashes for the cases where a stable name needs to track the
current version. Together they give the platform an immutable foundation
for graph definitions and a mutable surface for execution.

**Cascade.** When a node's outputs change, cascade decides which
dependents become stale and recompute. The walk is driven by per-node
subscriptions declaring `type:` (a canonical signal type-path under
`terminal/*`, `transient/*`, `attribute/*`, or `message/*`)
and an optional CEL `when:` predicate over the signal payload. Subscriber
match is itself the cascade-fire gate; senders emit signals, receivers
decide. This is the killer primitive: it makes a graph reactive without
making the executor responsible for routing. Reactivity to external
change (sensors sending messages) is the same machinery as reactivity
to internal change. In service to the watching-external-state pattern.

**Claims and locks.** A claim is a node's request to access a
producer-managed resource, declared as a scope and resolved at runtime
by the producer. The platform serializes conflicting acquisitions
through the producer's conflict matrix; named locks are a deployment-
level capacity primitive declared in config. Together they are the
concurrency surface, and they are the primitive that makes coordinating
agents against shared infrastructure tractable. In service to the
shared-state coordination pattern.

**Held subgraphs.** Multiple nodes can share a single acquired claim
via a `holds:` directive. The claim resolves once at the end of the
holding subgraph: aggregate success commits, any failure abandons. This
is the atomic-staging machinery — the stage-then-promote-or-discard
pattern as first-class. The producer's commit verb runs exactly once;
the abandon verb is the rollback. In service to the atomic-subgraph
pattern.

**Fan-out and run-tree.** A node can partition a held claim into
sub-claims at runtime via the producer's split-scope verb. Each
sub-claim gets its own run, tracked through a self-referential
parent/child-key tree on the per-run record. Fan-out is the hook that
makes data-shaped work tractable — partition-per-key, materialize-per-
partition, aggregate-back-up — without baking partition semantics into
the platform. The platform sees a tree of runs and claims; the producer
decides what a partition means.

**Service protocols.** Out-of-process gRPC services implement one or
more rimsky protocols: claim producer (resource acquisition), executor
(work dispatch), lifecycle subscriber (template/instance state-
transition hooks), publisher (the external-trigger surface, of which
sensors are one class), and the optional `Validation` mix-in for
template-registration-time checks. The executor primitive's scope is
broader than the name suggests — anything that takes inputs and
produces outputs can be an executor. An agent, a CI pipeline, a webhook
dispatcher, a Lambda behind API Gateway, a transformation written in
Python — wrapped behind the executor protocol they all participate in
rimsky's claims, attributes, error policy, cascade, frames, and parked-
state machinery on equal footing. This is the primitive where the
agentic framing is most visible.

**Assets and content lineage.** Durable-lifetime claims surface as
assets — addressable resources with stable identity and provenance
records. Two lineage record kinds (`leaf_run` for computational
provenance, `claim_terminal` for data-promotion provenance) feed the
control-api's lineage endpoints. This is not a data-engineering
primitive: it is the way agentic work gets durable handles and audit
trails. When an agent produces a result that needs to outlive its
frame, it lands as an asset; the lineage chain shows what produced it
and what consumed it.

**Control-plane MCP skin and API-key auth.** Every operator action on
the control-api is also an MCP tool at `POST /mcp`, so an LLM-driven
supervisor can drive the platform on the same verbs a human would.
Authentication is per-key bearer tokens with JSONB permission grants,
verb-noun action grammar with wildcard support, per-handler dry-run
mode, and structured audit on the existing events log. The bootstrap
path is implicit-anonymous-admin until the first real key is minted.
This is the agent-operator surface — the answer to "an LLM is going to
operate this platform on behalf of a human or as part of a higher-level
agentic system."

For the formal definitions of each primitive — purpose, boundaries,
and the code sites that cite them — read
`.ok-planner/design/concepts.md`. It is an auto-generated TOC over a
file-per-concept catalog and is the durable design surface. Inline
`@concept:` annotations in the source link enforcement sites back to
the catalog.

## 5. What rimsky deliberately isn't

The platform stays useful by being explicit about what it is not. If a
future direction crosses one of these lines, it gets pushed into the
consumer's domain or to a more appropriate adjacent system. This is a
feature, not an apology.

**Stream processing.** Event-time windowing, watermarks, late-data
handling, exactly-once stream semantics. These are streaming data-plane
concerns. Pulling them into rimsky would force the orchestrator to
take on data-plane responsibilities and erode the store-agnostic
position.

**Per-key state stores.** Keyed state of the kind that streaming
frameworks model as first-class. Same reason — application state
belongs to the application, not the orchestrator.

**Streaming-batch unification.** Rimsky's invocation model is discrete
dispatch. A node either runs or doesn't; it does not represent a
continuously-running stream operator. There is no plan to make discrete
dispatch and continuous streaming look uniform.

**CPU/memory-aware cluster scheduling, fair-share queueing, cluster
resource management.** These are cluster scheduler concerns. Rimsky's
named-lock primitive gives basic capacity gating; deeper scheduling
lives downstream in whatever orchestrates the rimsky processes
themselves.

**Semantic layer, metric definitions, SQL transformation language.**
Application-level concerns. Rimsky does not model what a "customer" is,
what a metric definition looks like, or how transformations should be
expressed. A consumer can put any of those on top of rimsky; rimsky
will not grow one.

**In-flight workflow migration.** Templates are content-addressed and
tags are movable; old instances continue on their template hash and new
instances pick up the moved tag. The remaining mid-flight-migration-to-
a-new-template-version concern is not a planned primitive.

**Bundled agent-pattern libraries.** Knowledge stores, supervisor
templates, meta-agent primitives. These were considered and declined —
pre-v1 bundling would lock in opinions about entry shape, scope
conventions, and supersession semantics before any real consumer has
stressed them. The existing primitives (claim producer, lifecycle
subscriber, MCP, executor) support any consumer who wants to build
them in their own repository.

**A SQL data platform.** The typed-attribute system, partitions, and
lineage exist to make agentic work durable, auditable, and partitioned
where partitioning matters. They do not make rimsky a data platform.
There is no transformation language, no semantic layer, no metric
service, no query optimizer, no scheduled-table-build abstraction. If
data-engineering-as-product is what you need, an adjacent system is
the right tool.

When a primitive starts to look like it would solve a problem on one of
these lines, the resolution is to push the problem into a consumer-side
service or an adjacent system, not to grow the primitive.

## 6. First-steps walkthrough

A new operator on a freshly-pulled checkout exercises the dev loop end
to end without writing a template from scratch, using the onboarding
template the test suite drives, against a local all-in-one stack.

1. Bring up the stack and the bundled verifier-shape-checks executor.
   The simplest path is `make core-images && make service-images` to
   build the local images, then `docker run` the all-in-one image with
   `verifier-shape-checks` declared in its `rimsky.yml`. For an
   automated bring-up, the driver test under
   `lib/services/test/scenarios/onboarding_demo_e2e_test.go` wires both
   via testcontainers and runs the walkthrough end-to-end — it is the
   load-bearing gate that this walkthrough actually works.
2. Run the template against the live endpoint:

   ```sh
   rimsky run test/fixtures/demos/onboarding-template.yaml
   ```

   This is the headline dev-loop verb: register + deploy + create in
   one shot against a TemplateSpec. On success it prints
   `instance_id=<uuid>` and exits 0.
3. Watch the instance progress to a terminal state:

   ```sh
   rimsky watch <instance_id>
   ```

   The driver test above runs `test/fixtures/demos/onboarding-demo.sh`
   as a subprocess — it wraps both verbs and exits 0 once the instance
   terminates — which is how the dev loop stays gated.

That TemplateSpec references the bundled verifier-shape-checks
executor and embeds an inline three-row dataset, so the dispatch runs
real verification work (`no_nulls` + `pk_unique`) and reaches a
terminal Success without the operator editing anything.

## 7. Where to learn more

For agents: point your coding agent at this repo and ask. The source,
the protocol definitions under `lib/protocols/proto/v1/`, and the concept
catalog below are the canonical surfaces.

For the concept catalog: `.ok-planner/design/concepts.md` is an
auto-generated TOC over the per-concept files under
`.ok-planner/design/concepts/`. Each file carries a definition, a
purpose, and boundaries. Inline `@concept:` annotations in the source code
point at enforcement sites.

For protocols: the wire definitions live in this repo under
`lib/protocols/proto/v1/`.

Point your coding agent at this repo and ask.

## 8. License

Rimsky is multi-licensed. The `lib/protocols/` module — the wire contract a
consumer implements or links against — is Apache 2.0. Everything else Rimsky
ships, the orchestrator binaries and the reference services, is
AGPL-3.0-or-later or, by agreement, a Fall Guy Consulting commercial license.

See [`COPYING.md`](COPYING.md) for the full explanation, the rationale behind
the split, and how to comply. The binding texts are `LICENSE.apache`,
`LICENSE.agpl`, and `COPYRIGHT`.
