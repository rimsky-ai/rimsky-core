# Rimsky vs. the orchestration landscape

A positioning analysis. Not a competitive teardown — the goal is to identify
where Rimsky sits in the existing market, what shape of problem it actually
solves, what other projects it could absorb as components, what other projects
solve adjacent problems better, and how its open-source posture differs from
the prevailing open-core squeeze.

This doc is a snapshot, current as of 2026-05-02. It draws on Rimsky's own
design docs (`docs/architecture.md`, `docs/node-graph-design.md`,
`docs/glossary.md`) and on web research for each compared project (citations
inline).

---

## 1. What Rimsky is, in two paragraphs

Rimsky is a project-agnostic **reactive node-graph orchestration platform**.
Work is modeled as a graph of *nodes* that communicate via two messages
(`invalidate`, `recalculate`) and operate on operator-configured *stores*
through a 4+1-verb gRPC protocol (`Open` / `Commit` / `Abandon` / `Release` +
`Capabilities`). Nodes execute through *executors* — peer services in any
language that speak the node-executor protocol (gRPC + HTTP+JSON bridge).
State of truth lives in Postgres; templates are content-addressed
(`sha256-<hex>` over an RFC 8785 JCS canonicalization); cascade resolution is
*frame*-bracketed for deterministic per-instance progress.

The vocabulary is deliberately small: 2 message types, 4 node states
(`fresh`, `stale`, `running`, `failed`), 3 error actions (`retry`,
`invalidate(targets)`, `give_up`). Three architectural collections — the
orchestrator (Go), reference stores under `stores/<kind>/`, reference
executors under `executors/` — ship in one repo today and are designed to
separate cleanly. Stores and executors are out-of-process; the orchestrator
itself is three independent long-running processes (`rimsky-scheduler`,
`rimsky-supervisor`, `rimsky-control-api`) that communicate **only** through
Postgres.

---

## 2. The shape question

The orchestration market clusters into three families. Most positioning
arguments collapse if you don't first place a project in the right family.

| Family | What it orchestrates | Who's there |
|---|---|---|
| **Pipeline orchestrators** | Forward-only DAGs of data assets / tasks on a schedule. | Airflow, Dagster, Prefect, Kestra |
| **Durable execution engines** | Long-running imperative workflows; engine replays history to recover. | Temporal, Restate, Inngest |
| **Agent frameworks** | LLM-driven agents and conversations, optionally with state. | LangChain/LangGraph, Microsoft Agent Framework, AutoGen, CrewAI |
| **Agent harnesses** | A single programmable agent runtime (model + tools + sandbox + filesystem); replaces Claude Code / Codex / Cursor as headless code-acting agents. | Flue (Astro org, Apache-2.0, Nov 2026) |

Rimsky is **none of these three**. Its primitive is a **reactive node graph
with engine-derived upstream invalidation**: a node committing
`changed: true` cascades a `recalculate` to its dependents; a downstream
observation can fire `invalidate(targets)` against an upstream node and force
its re-execution; the scheduler then drives the cascade to a clean per-instance
*frame* close. Every other system in the table above either runs forward only
(pipeline orchestrators) or runs whatever control flow the workflow author
codes (durable engines, agent frameworks). None makes
`fresh / stale / running / failed` and `invalidate / recalculate` the
engine-level vocabulary.

This is the load-bearing differentiator. Skip past it and the rest of the
comparison loses its anchor.

---

## 3. Family-by-family teardown

### 3.1 Pipeline orchestrators

#### Dagster

- **Conceptual model.** Software-defined assets (the recommended primary
  abstraction) over an underlying op/job graph; **forward-only DAG**.
  Declarative Automation evaluates `AutomationCondition`s on a daemon tick;
  what looks reactive is daemon-polled, not engine-derived.
- **Programming model.** Python-first, decorator-driven, in-process by
  default. Dagster Pipes (JSON-over-stdio) is the launch-and-report escape
  hatch for non-Python work — Python client is production; Rust client is a
  proof of concept; Go/JS not first-class.
- **State.** Pluggable `DagsterInstance` storage; Postgres standard. Run
  isolation depends on the launcher (Docker / K8s / ECS survive daemon
  restart; in-process executor does not).
- **OSS vs commercial.** Open-core. OSS is the framework + integrations +
  basic UI. Dagster+ paywalls branch deployments, RBAC, SSO, audit, column-
  level lineage, asset catalog search, code-location quotas, and is
  credit-metered on top of seat fees ($0.035–$0.040 per asset materialization
  / op execution). Solo and Starter pricing rose May 2026. Community
  concerns about "where's the OSS-vs-Plus feature matrix?" are
  documented (dagster-io/dagster#25313).

  Sources: [pricing](https://dagster.io/pricing),
  [open-core post](https://dagster.io/blog/open-core-business-model-dagster).

- **Doesn't fit:** reactive cascades from downstream observations, long-
  running multi-step LLM agents, real-time / sub-second cycles, polyglot
  executor pools, embedding inside another orchestrator (Dagster wants to
  be the platform).

#### Prefect

- **Conceptual model.** Flows (Python functions) + tasks dynamically
  discovered at runtime. Trigger-based reactivity via Events + Automations
  (notably moved INTO OSS in 3.0). No replay-from-log determinism — failed
  flows resume from last task state.
- **Polyglot.** Python only at the flow level.
- **OSS vs Cloud.** Cloud paywalls SSO/SCIM, granular RBAC, hosted dashboard,
  long-term log retention. Trend has been to add managed-enterprise features
  to Cloud while keeping the orchestration core open.
  Source: [Prefect OSS comparison](https://www.prefect.io/compare/prefect-oss).

#### Airflow

- **Conceptual model.** DAG-of-tasks, scheduled by interval. Airflow 3.0
  added Assets + AssetWatchers — closer to event-driven, still not engine-
  derived invalidation.
- **Polyglot.** Python control plane; tasks shell out anywhere.
- **OSS vs commercial.** Apache. Vendors (Astronomer, MWAA, Cloud Composer)
  sell management, not features.
- **Pain points at scale:** metadata-DB lock contention (250 ms per task
  state transition, 2 s+ at ~800 concurrent tasks per Shopify's writeup),
  DAG-parse cost, XCom abuse for big data.
  Sources: [Shopify scale lessons](https://shopify.engineering/lessons-learned-apache-airflow-scale),
  [Prefect's hidden costs post](https://www.prefect.io/blog/hidden-costs-apache-airflow).

#### Kestra

- **Conceptual model.** YAML-declared workflows, ~1300 typed plugins, event
  triggers (Kafka, S3 polling, etc.).
- **Polyglot.** Strongest of this family at the task level — workflow defs
  are YAML; tasks run any-language code via script plugins.
- **OSS vs Enterprise.** Enterprise-only: HA + Kafka backend, RBAC, SSO,
  audit, multi-tenancy, namespace secrets + Vault/Key Vault, Worker Groups
  for infra isolation. OSS is single-server.
  Source: [Kestra OSS-vs-paid](https://kestra.io/docs/oss-vs-paid).

#### Where Rimsky differs from the pipeline family

- These all schedule *forward*; Rimsky cascades *both ways*. A downstream
  evaluator declaring "this output is wrong because of an upstream config
  problem" is a one-line `error_types` policy in Rimsky
  (`invalidate(['upstream_node'])`) — in any pipeline orchestrator it's a
  custom sensor + manual backfill operator-driven workflow.
- These are Python-first (Kestra excepted) at the *control plane*. Rimsky's
  control plane is Go; the executors are anything that speaks the protocol.
- These think in batches and schedules. Rimsky thinks in messages and
  states; schedules are nodes-with-cron, not a privileged scheduler concept.

### 3.2 Durable execution engines

#### Temporal

- **Conceptual model.** Workflow + activity. Workflow is a deterministic
  function replayed against an event-sourced history. Source of truth lives
  in the engine (Cassandra or MySQL/Postgres for the default store; Postgres
  or Elasticsearch for visibility).
- **Polyglot.** Per-language SDK (Go, Java, TS, Python, .NET, Ruby, PHP).
  Strong polyglot for *applications*; the SDK is the boundary for *workflow
  code*.
- **Limits.** 50 MB / 51,200 events per workflow execution; 2 MB max payload.
  LLM payloads need a payload codec offloading to S3 in practice.
  Sources: [workflow execution limits](https://docs.temporal.io/workflow-execution/limits),
  [render.com on durable agents](https://render.com/articles/durable-workflow-platforms-ai-agents-llm-workloads).
- **OSS vs Cloud.** Server is OSS and feature-equivalent to Cloud; Cloud
  gates operations (managed multi-region, namespace quotas, hosted UI, SLA).
  This is the cleanest open-core split in the comparison set.
- **Reactivity.** None native. Signals + branching + manual rewind logic if
  you want it.

#### Restate

- **Conceptual model.** Services / Virtual Objects (per-key serialization) /
  Workflows. Single Rust binary, RocksDB local + S3 snapshots, log is source
  of truth.
- **Polyglot.** TS, Java, Kotlin, Python, Go, Rust SDKs.
- **OSS vs commercial.** Runtime is BSL (the "Apache for everyone except
  hyperscalers reselling" license); SDKs are MIT. Restate Cloud is publicly
  available with usage-based pricing (durable actions). Source:
  [restate.dev](https://www.restate.dev/).

#### Inngest

- **Conceptual model.** Event-triggered step functions; durable memoization
  per step.
- **Polyglot.** TS-first, Python and Go SDKs in beta.
- **OSS vs Cloud.** Open-core engine + managed cloud. Free tier 50k
  executions / 24h trace retention; paid tiers add concurrency, retention,
  RBAC/SSO/audit. Source: [inngest.com/pricing](https://www.inngest.com/pricing).

#### Where Rimsky differs from the durable family

- All three are imperative: the workflow code IS the spec; the engine
  replays the history. Rimsky is declarative: nodes + dependencies +
  policies; the cascade is engine-derived from state, not from replayed
  control flow.
- All three terminate per workflow. Rimsky's primitive is an *instance* that
  may have many cascades (frames) over its lifetime; nodes go
  `fresh → stale → running → fresh` repeatedly.
- All three want to *be* the orchestration. Rimsky wants to dispatch *into*
  them when one of them is the right tool for an individual node's work.

### 3.3 Agent frameworks

#### LangChain + LangGraph

- **Conceptual model.** LangChain was chains/agents in Python or JS; the
  library has been re-anchored on top of LangGraph. LangGraph is a Pregel-
  inspired `StateGraph` with first-class **cycles**, conditional edges, and
  reducer-merged state channels. Execution is super-step ticks.
- **State.** Checkpointer-backed (`InMemorySaver`, `SqliteSaver`,
  `PostgresSaver`, Redis, Mongo, Cosmos). Per-thread state of truth, but no
  cross-thread orchestration metadata at the OSS layer.
- **Polyglot.** Python and JS SDKs only. Nodes are language-level callables
  in the host runtime. `RemoteGraph` lets you compose multiple LangGraph
  deployments — you can't register a Go/Rust service as a first-class node.
- **OSS vs commercial.** Libraries are MIT. **LangSmith Deployment**
  (formerly LangGraph Platform — renamed Oct 2025, folding the runtime under
  the paid observability brand) is the production answer. Self-Hosted Lite
  is free up to 1M nodes executed; past that, Self-Hosted Enterprise (paid).
  Self-hosting LangSmith itself is Enterprise-only. The Aegra project
  ([aegra.dev](https://www.aegra.dev/)) exists specifically as an
  Apache-2.0 drop-in replacement, citing that BYO-Postgres / self-hosting /
  custom auth are gated. HN sentiment on the per-trace + per-seat pricing
  and the LangSmith coupling is consistently negative
  ([HN 44840323](https://news.ycombinator.com/item?id=44840323),
  [HN 40739982](https://news.ycombinator.com/item?id=40739982)).
- **Doesn't fit:** polyglot executor pools, multi-tenant production
  orchestration without paying for Platform, reactive cross-thread cascades,
  domain-agnostic non-LLM work.

#### Microsoft Agent Framework

- **Conceptual model.** Merge of Semantic Kernel + AutoGen, GA April 2026.
  Two layers: single Agents (LLM + tools + MCP) and Workflows (graph of
  Executors with conditional Edges, parallel super-steps, checkpointing).
  In-process SDK; .NET and Python first-class.
- **State.** Per-component pluggable: Foundry Agent Service memory, Mem0,
  Redis, Neo4j, custom. No canonical state-of-truth.
- **OSS vs commercial.** MIT. Monetization is indirect — the framework is
  the on-ramp to Azure AI Foundry consumption. Foundry Agent hosted runtime
  is the recommended managed path.
- **Doesn't fit:** polyglot beyond .NET/Python, reactive dependency
  invalidation, multi-tenant orchestration tier, durable multi-day workflows
  with operator visibility independent of the hosting layer.

#### AutoGen

- **Status.** Maintenance only; superseded by Microsoft Agent Framework.

#### CrewAI

- **Conceptual model.** Roles + Tasks + Crews; added Flows (event-driven
  pipelines) for production-style workloads. Python only.
- **OSS vs commercial.** MIT core + AMP Cloud SaaS + AMP Factory enterprise.
  Free tier 50 executions/month; paid from ~$99/mo + per-execution overage;
  Enterprise reportedly six figures.
  Source: [ZenML CrewAI breakdown](https://www.zenml.io/blog/crewai-pricing).

#### The agent-framework common thread (continued below)

### 3.4 Agent harnesses

A new family that's worth distinguishing from agent *frameworks* —
exemplified by **Flue** (launched Nov 2026 by the Astro org;
[flueframework.com](https://flueframework.com/),
[github.com/withastro/flue](https://github.com/withastro/flue)).

#### Flue

- **Conceptual model.** Not a graph at all. A fixed 4-layer stack: Model
  (tokens / tools / prompts) → Harness (skills / memory / sessions) →
  Sandbox (bash / security / network) → Filesystem (read / write / grep /
  glob). The unit of work is an **agent invocation** with a **session** of
  message history, optionally fanning out to child tasks via
  `session.task()`. The pitch is "Claude Code, but 100% headless and
  programmable." Flue replaces Dosu / Greptile / CodeRabbit, not Temporal
  / Dagster / LangGraph.
- **Programming model.** TypeScript-only. Agents are TS modules exporting
  `triggers` (webhook / CLI) and a default handler that takes a
  `FlueContext`. Markdown-first authoring — skills in `.agents/skills/`
  plus an `AGENTS.md` spec — with typed results via Valibot. No YAML, no
  decorators, no class hierarchies.
- **State.** Per-session message history, keyed by `agent-id + thread`.
  Backed by Cloudflare Durable Objects on CF, in-memory on Node.
  Workspace state lives on a mounted filesystem (R2 buckets on CF, host
  FS locally, container FS in Daytona mode). **No central state-of-truth
  across sessions.**
- **Polyglot.** No. TypeScript-only for agent code; sandboxes can shell
  out to any binary.
- **OSS vs commercial.** Apache-2.0. No pricing page, no managed cloud,
  no paid tier currently advertised. Self-host only. Marked
  "Experimental — APIs may change." Backed by Astro
  ([HN launch](https://news.ycombinator.com/item?id=47988501)).
- **Doesn't fit:** multi-node reactive orchestration; cross-session
  state-of-truth; polyglot executor pools; lock primitives across
  concurrent agent runs; engine-derived invalidation.

#### Where Rimsky differs from Flue

The two projects barely overlap. Both run agent-shaped work in
sandboxed environments with retries and structured outputs, and both are
positioned to land in `.claude/`-adjacent local-dev contexts. But the
architectures are inverted:

| Dimension | Rimsky | Flue |
|---|---|---|
| Primary unit | Reactive node in a graph with cascading invalidation | Agent session |
| Re-execution model | Engine-derived `invalidate(targets)` from downstream policy; `fresh / stale / running / failed` | Re-prompt within a session, or restart session |
| Process model | 3 long-running Postgres-mediated processes + out-of-process executors + out-of-process stores | Per-invocation HTTP handler or CLI |
| State of truth | Postgres (`rimsky_dispatch`, `rimsky_lock_holders`, content-addressed templates) | Session message history |
| Polyglot | gRPC + HTTP+JSON executor protocol — any language | TypeScript only |
| Templates | sha256-content-addressed with movable tags | None — code is the spec |
| Multi-agent coordination | Region claims + named locks across nodes | Subagent fan-out via `session.task()`, no cross-session locks |

The cleanest framing: **Flue is a programmable agent harness; Rimsky is
an orchestrator that calls programmable agent harnesses.** A Rimsky
template that drives a multi-step refactor cascade can dispatch each node
to a Flue agent (via `http-node` against Flue's webhook trigger, or via a
purpose-built Flue executor), with Rimsky owning the inter-agent
reactivity, the locks on shared workspace state, the per-instance frame
close, and the audit log. Flue owns the within-agent reasoning, the
sandboxing, and the file-acting primitives. The two compose naturally
because they don't compete for the same architectural slot.

This is also the most direct empirical confirmation of §10.3's
"agent harness vs orchestration template" framing: a credible new
project shipped at the harness layer, with an explicit non-positioning
against orchestration, in the same week this comparison doc was written.
The market is segmenting along that line.

#### The agent-framework common thread

All four are **single-process, single-language, agent-shaped**. The mental
model is LLM agents, conversations, role-playing, tool calls — not nodes
with dependencies. State is in-memory by default with checkpointing as an
add-on. The "graph" capabilities (LangGraph, MAF Workflows) are still
single-process executor graphs. None is positioned as a multi-tenant
production orchestrator; that's where their hosted commercial products
come in.

---

## 4. Capability matrix

Reading: ✓ = first-class engine primitive; ◐ = possible via user code or a
trigger plugin; ✗ = absent / actively against the model.

| Capability | Rimsky | Dagster | Prefect | Airflow | Kestra | Temporal | Restate | Inngest | LangGraph | MAF | CrewAI | Flue |
|---|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|
| Reactive upstream invalidation (engine-derived) | ✓ | ✗ | ◐ | ✗ | ◐ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| Cyclic / repair execution within a single instance | ✓ | ✗ | ◐ | ✗ | ✗ | ◐ | ◐ | ◐ | ✓ | ◐ | ◐ | ◐ |
| Polyglot executors (any language, wire protocol) | ✓ | ◐ | ◐ | ◐ | ✓ | ◐ | ✓ | ◐ | ✗ | ✗ | ✗ | ✗ |
| State-of-truth in shared transactional DB | ✓ | ✓ | ✓ | ✓ | ◐ | ✓ | ✓ | ✓ | ◐ | ✗ | ✗ | ✗ |
| Out-of-process executors | ✓ | ◐ | ◐ | ✓ | ✓ | ✓ | ✓ | ✓ | ◐ | ✗ | ✗ | ✗ |
| Pluggable storage backends as first-class peers | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| Lock primitives at the orchestrator level | ✓ | ✗ | ◐ | ◐ | ◐ | ◐ | ✓ | ◐ | ✗ | ✗ | ✗ | ✗ |
| Content-addressed templates / spec hashing | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| Multi-tenant isolation in OSS | ✓ | ✗ | ✗ | ◐ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ |
| Long-running multi-step LLM agents | ◐* | ✗ | ◐ | ✗ | ◐ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Asset / lineage UI | ◐ | ✓ | ◐ | ◐ | ◐ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| Sandboxed code-acting agent runtime | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ◐ | ◐ | ◐ | ✓ |

\* Rimsky's executor model can host any agent framework as a black-box
executor. The "agent-shaped" reasoning is not Rimsky's primitive — it's
what executors do.

The pattern: Rimsky is the only row where the *reactive*, *polyglot*,
*locks-as-primitive*, and *multi-tenant-in-OSS* cells are all checked. None
of the others are *trying* to be that — they're optimizing for different
shapes of problem.

---

## 5. Composability — what Rimsky encompasses, sits alongside, or is alternative to

The most useful framing for adopters: don't ask "which one wins?" Ask
"what's the right shape for this slice of work, and what calls what?"

### 5.1 Encompasses (good as Rimsky executors)

These are tools whose strength is *intra-node work* and which expose a
clean synchronous-or-callback I/O contract. A Rimsky executor wraps each;
the executor is the integration boundary.

| Project | Wrapping shape | What Rimsky brings |
|---|---|---|
| **LangGraph** | Executor hosts a compiled graph; takes input + thread_id, streams to completion via async callback. | Reactive upstream invalidation (LangGraph thinks per-thread); multi-tenant isolation; polyglot peers; lock primitives. |
| **CrewAI** | Executor calls `crew.kickoff(inputs={...})`; returns final output as commit payload. | Same. |
| **Microsoft Agent Framework** | Executor calls `workflow.run(...)` or `agent.run(...)`; MAF's `.as_agent()` shape composes naturally. | Same. |
| **AutoGen** | Same as CrewAI — wrap a GroupChat completion. | Same. |
| **Temporal** | Executor starts a Temporal workflow; uses a signal as ack or polls workflow status; final result is the commit payload. Temporal handles intra-node retry/saga; Rimsky handles inter-node reactivity. | Inter-node reactivity; engine-derived invalidation; the per-instance frame model. |
| **Restate** | Same shape as Temporal but lighter. Restate's per-key serialization could even map cleanly to Rimsky's named locks if you wanted to de-duplicate. | Same. |
| **Inngest** | Awkward direction-of-control (Inngest is event-pull-based; Rimsky is dispatch-push) but mechanically fine via the existing async-callback path. | Same. |
| **Flue** | Executor (`http-node` against Flue's webhook trigger, or a purpose-built Flue executor) hosts a Flue agent invocation; takes input as the agent's session start payload, returns the agent's final structured output as the commit payload. | Inter-agent reactivity; cross-session locks on shared workspace state; per-instance frame close; audit log; the polyglot peer story for non-Flue work. |

The strongest single framing for the comparison doc: **agent frameworks
are domain-specialized executors; Rimsky is the substrate they'd run inside
in a serious production deployment.**

### 5.2 Sits alongside

| Project | Why it doesn't compose either way |
|---|---|
| **Dagster** | Wants to be the platform — owns daemon, run launcher, instance state, UI, code-location server. The asset graph isn't a portable building block. Pipes only goes outward (Dagster launches → external code reports back). The natural relationship is "Dagster handles your asset/lineage/cost story, Rimsky handles your reactive node graph, they coexist." |
| **Prefect / Airflow / Kestra** | These are themselves orchestrators with their own scheduling-of-truth. Layering them under Rimsky creates two reactive layers; the impedance mismatch isn't worth it. Best treated as alternatives for the *batch-on-a-schedule* shape, which Rimsky is not optimizing for. |

### 5.3 Alternative to (different shape)

Pipeline orchestrators (Airflow, Dagster, Prefect, Kestra) are the right
call for: scheduled batch ETL with stable graph shape, partitioned
backfills, asset lineage as the actual product, dbt-shaped warehouse work.
Rimsky is the *wrong* tool for these problems — its declarative cascade
model doesn't help when "reactivity" is just "run the same DAG every six
hours and recompute partitions."

---

## 6. OSS vs commercial — the open-core squeeze, and Rimsky's posture

A pattern across the comparison set:

| Project | OSS surface | What's paywalled | Pattern |
|---|---|---|---|
| Dagster | Framework, integrations, basic UI | Branch deployments, RBAC, SSO, audit, lineage UI, asset catalog search, code-location quotas, *credit metering on every materialization* | "Born paid" enterprise + UX features + per-execution metering |
| Prefect | Orchestration core, events, automations | RBAC, SSO/SCIM, hosted dashboard, retention | Standard managed-enterprise gating |
| LangChain Inc. | `langchain`, `langgraph` libraries (MIT) | LangSmith observability (self-hosting Enterprise-only, 14d retention free), LangSmith Deployment runtime (1M nodes free, then paid), durable runtime, custom auth | **Renaming the runtime under the observability brand** is the strongest signal of "the OSS is the on-ramp" |
| CrewAI | Framework (MIT) | AMP Cloud SaaS, AMP Factory enterprise | Open-core SaaS + per-execution overage |
| Inngest | OSS engine | Concurrency, retention, RBAC/SSO/audit, payload size | Standard freemium |
| Kestra | Single-server engine | HA + Kafka backend, RBAC, SSO, audit, multi-tenancy, secrets | "OSS is dev / Enterprise is prod" — the heaviest-handed of the bunch |
| Temporal | Server (feature-equivalent) | Managed multi-region, namespace quotas, hosted UI, SLA | Cleanest split — Cloud sells *operations*, not features |
| MS Agent Framework | Full SDK (MIT) | Foundry hosted runtime, Foundry Agent Service memory, Azure AI services | Indirect monetization via Azure consumption |

### 6.1 Where Rimsky should land

Rimsky's stated intent (per `README.md` §License) is "permissive open-source
at v1 ship." The question is what stays open. The honest answer should
match Temporal's split — feature-equivalent OSS, with any future commercial
offering selling *operations* rather than *features*. Specifically:

- **Stay OSS forever:** the orchestrator (scheduler / supervisor / control-
  api / migrate / conformance), the protocol, the `Store` interface, the
  reference store-services (filesystem, postgres, stub), the reference
  executors (http-node, claude-agent), all blessed invariants and the
  scenario test suite, the Helm chart and Docker Compose deployment, the
  conformance binary.

- **Plausibly commercial later:** managed multi-region operation, hosted
  observability over `rimsky_events`, an SLA, namespace-quota tooling for
  multi-tenant SaaS deployments, enterprise SSO / RBAC bolted onto the
  pluggable `Authenticator`, premium store-services backed by proprietary
  storage tech.

- **Never commercial:** the protocol, the template grammar, lock primitives,
  the cascade model, the frame engine, the auto-terminal mechanism, any of
  the 20 blessed invariants. Anything that an executor or store author would
  have to know about to be conformant must stay open.

This split is enforceable by structure: the three-collection separation
(orchestrator / stores / executors) means the wire protocol is documented
and conformance-tested; a future commercial control plane has to speak the
same protocol the OSS one does, or it isn't Rimsky.

### 6.2 The "ongoing OSS alternative" framing

The most defensible market positioning, and the one that aligns with the
project's actual shape:

> **Rimsky is the unmonetized substrate for agentic and reactive workflows.
> Agent frameworks (LangGraph, CrewAI, MAF) and durable engines (Temporal,
> Restate, Inngest) run as executors on top. Pipeline orchestrators
> (Dagster, Prefect, Airflow, Kestra) solve a different shape of problem.
> The orchestrator, the protocol, and the reference implementations stay
> permissive OSS forever.**

This works because:

- It doesn't require Rimsky to *beat* anything. Each comparison project
  retains its niche; Rimsky absorbs them as components or sits alongside
  them.
- It addresses the documented community pain — Aegra's existence as a
  LangGraph Platform alternative is direct evidence that there's demand for
  "the runtime stays OSS."
- It uses the protocol-first architecture as a credibility signal. A
  protocol with a public conformance test suite is a much harder commitment
  to walk back than a Python library.

---

## 7. The "Docker for agentic workflows" framing

Docker's analogy was: containers are a portable bundle of *application +
runtime requirements*; the Docker daemon is a portable substrate for
running them; the registry is a portable distribution mechanism. The
ecosystem around Docker (Compose, Kubernetes, build tools) grew because the
container abstraction was small, well-specified, and unmonetized at the
core.

The mapping to Rimsky:

| Docker | Rimsky |
|---|---|
| Image (declarative bundle) | Template (content-addressed spec) |
| Container (running instance) | Instance |
| Image registry | Template registry (`rimsky_templates` + tag aliases) |
| `docker run` | `POST /v1/instances` |
| Daemon (per-host runtime) | Three-process orchestrator |
| `docker-compose.yml` (multi-container deployment) | `rimsky.yml` (multi-store, multi-executor deployment) |
| Volume / bind mount | Store + claim |
| Container runtime spec (OCI) | Node-executor protocol + Store-service protocol |
| `docker exec` / sidecar | Inheritance / claim-pass |

The framing works **only if** the protocol is the load-bearing artifact and
the reference implementations are the proof-of-portability. It fails if
Rimsky becomes "the orchestrator that ships with a great set of in-process
features." The OCI spec mattered more than `dockerd` ever did; Rimsky's
proto/v1 must matter more than the Go reference orchestrator does.

Practical implications:

- **Conformance is a first-class deliverable.** `rimsky-conformance` already
  exists for executors. A `rimsky-store-conformance` binary should exist on
  the same footing for store-services. Any future commercial offering — by
  Rimsky's authors or anyone else — must pass conformance to call itself
  Rimsky.
- **The protocol has its own changelog.** `proto/v1/*.proto` should be
  versioned independently of the Go module. Breaking changes are v2,
  signaled across the ecosystem.
- **Store-services and executors should be encouraged in any language.**
  The TS `claude-agent` already exists; a Python reference store-service
  and a Rust reference executor would massively reinforce the framing.

Caveats this framing has to acknowledge:

- Docker had a much narrower scope (run a process in a sandbox) than
  Rimsky does (orchestrate a reactive node graph across heterogeneous
  stores and executors). The analogy is positioning, not literal
  equivalence.
- Docker also had the advantage of solving a problem most engineers had
  already felt the pain of. Rimsky's reactive-cascade pain is real but less
  universally felt — most teams never hit it because their pipelines are
  forward-only. The framing has to lead with "if you've ever wished a
  downstream evaluation could revise an upstream config, this is for you."

---

## 8. Headline positioning options

Three candidates, in order of risk-adjusted fit. They are not mutually
exclusive — the doc can lead with one and reference the others.

### 8.1 "The unmonetized substrate for reactive agentic workflows" (recommended)

Positions against the open-core squeeze without naming names. Promises
permanent OSS for the orchestrator, the protocol, and reference
implementations. Acknowledges that other tools may be the right call for
specific shapes (Dagster for assets; Temporal for durable orchestration;
LangGraph for in-process LLM reasoning) and embraces them as components.

Risk: makes a license commitment Rimsky has to keep. The license file
should land at v1 with an explicit "if we ever fork this into a commercial
product, the protocol and reference implementations stay Apache-2.0 / MIT"
clause.

### 8.2 "Docker for agentic workflows"

Punchier, viral-friendlier, but trades some accuracy for memorability. Use
as a one-liner; back it with the analogy table from §7. Best paired with
8.1 — the two reinforce each other (Docker stayed OSS at its core; Rimsky
intends to too).

Risk: invites "but Docker is X and Rimsky isn't" objections. Works only if
the protocol/conformance story is genuinely there.

### 8.3 "The reactive node-graph orchestrator"

Most accurate, least exciting. Use in technical docs and conference talks.
Pairs with the shape-based teardown in §3.

Risk: nobody knows what a reactive node graph is, so this only works after
the reader has been primed by one of the above framings.

---

## 9. Open questions worth resolving before this goes external

1. **License commitment.** What license, and what protocol-stability
   commitment, will land at v1 ship? Apache-2.0 is the default that
   matches the framing.
2. **Conformance for store-services.** `rimsky-conformance` covers
   executors. A symmetric `rimsky-store-conformance` should exist before
   the "any-language store-service" framing is credible.
3. **The third reference executor.** `http-node` (Go) and `claude-agent`
   (TS) are two languages. A Python reference executor would meaningfully
   reinforce the polyglot story — Python is where the agent-framework
   adopters already live.
4. **The third reference store-service.** Filesystem, postgres, stub. An S3
   or git store would demonstrate the protocol's reach beyond
   "filesystem-shaped" backends.
5. **Public roadmap for the protocol.** A `proto/v1/CHANGELOG.md` separate
   from the orchestrator's CHANGELOG would signal that the protocol has its
   own lifecycle and is not just an artifact of the current Go
   implementation.
6. **What Rimsky deliberately won't do.** A short "non-goals" section in
   the README ("Rimsky is not an asset-lineage UI; not a durable workflow
   engine; not an agent framework") would make the positioning self-
   reinforcing instead of leaving it to readers to infer.

---

## 10. Scenarios where Rimsky is the right tool

The §3 teardowns identify *what Rimsky is differently*. This section
identifies *when you'd actually reach for it*. Three classes; the third is
the most underrated.

### 10.1 Reactive cascade scenarios (the headline case)

The shape: a downstream observation reveals a problem with an upstream
artifact, and the right repair is "re-run the upstream that produced the
bad input," not "retry this step." Forward-only orchestrators force you to
model this as an out-of-band backfill triggered by a custom sensor; in
Rimsky it's a one-line `error_types` policy emitting `invalidate(targets)`.

Concrete examples:

- **Schema-drift pipelines.** An ingestion node consumes a CSV; a downstream
  validator notices the column set drifted; the policy fires
  `invalidate(['fetch-schema-config'])` so the next cascade re-derives the
  config from a fresh schema probe before re-ingesting.
- **Probe-driven pipelines.** A `probe-ingestion` node runs a small-N test
  against the full pipeline; failure routes back to the `prepare-config`
  node rather than retrying the probe with the same bad config (the
  `node-graph-design.md` §3.6 probes-are-write-region-holding-nodes
  pattern).
- **Evaluator-revised LLM prompts.** A judge node scores an agent's output
  against rubric criteria; on `score_below_threshold` it invalidates the
  prompt-construction node upstream so the next run rebuilds the system
  prompt with the failure mode encoded as a constraint. Forward-only
  orchestrators retry the agent against the same prompt; Rimsky reaches
  back.
- **Config-validity drift.** A SaaS integration's API key rotated mid-run;
  the auth-failure error class invalidates the credential-fetch node
  rather than retrying with the stale token.

The diagnostic: if your incident postmortems ever contain the phrase
"...so we manually re-ran step 3 with new inputs," you're feeling Rimsky's
shape.

### 10.2 Long-running, easy-to-deploy, durable workflow scenarios

These are not unique-shape scenarios — Temporal/Restate/Inngest can do
them. But Rimsky is genuinely the path of least resistance for a specific
slice:

- **Workflows that already need a Postgres anyway.** Temporal needs
  Cassandra or a separate Postgres + Elasticsearch deployment; Restate
  brings RocksDB + S3 snapshots; Inngest is SaaS-first. Rimsky needs *one
  Postgres*, the same one your app already uses. `docker compose up` to a
  working stack in under 60 seconds (per `architecture.md` §9.4).
- **Workflows where a node IS a long-running async LLM agent.** The
  async-handoff protocol — executor returns `AsyncAccepted`, posts back
  via callback URL — is built in. The supervisor holds the dispatch claim
  and node state for the full async window with heartbeat-loss as
  backstop. The TS `claude-agent` executor exists as the reference
  implementation.
- **Workflows that need lock semantics at the orchestrator level.** Named
  locks (`limit: N`) for global concurrency caps (e.g., "at most 3
  concurrent OpenAI batch jobs"); region claims for "no two writers on the
  same S3 prefix simultaneously." Temporal's per-key serialization can
  approximate this; Restate's virtual objects fit closely; everything else
  in the comparison set leaves it to user code.
- **Workflows with cron + reactivity in the same graph.** A scheduled node
  fires every 6 hours; downstream nodes can also be invalidated *between*
  scheduled fires by a webhook-triggered control-API call. The schedule is
  just another node property; cron is not a privileged scheduler concept.
  In Airflow / Prefect / Dagster the "ad-hoc reactive trigger" sits in a
  different mental model than the schedule; in Rimsky they're both
  invalidates.
- **Workflows that need to survive a single-host deploy.** Rimsky's three
  processes (scheduler, supervisor, control-api) are stateless except for
  Postgres; you can `docker compose down && up` mid-run and active claims
  resume at the orphan-reap interval (default `5 × heartbeat_interval`).
  The "what survives a deploy?" answer is "everything in Postgres," which
  is auditable.

The diagnostic: if your durability requirements are "must survive process
restart and a few minutes of downtime, with sub-minute recovery," and you
already run Postgres, the operational cost of standing up Temporal is
hard to justify.

### 10.3 Local dev and document-maintenance scenarios (the underrated case)

This is where the framing shifts most sharply from the existing landscape.
The current pattern for "automate a repeatable Claude Code workflow" is to
write a skill — a markdown file that prompts Claude to do the right
sequence of steps. Skills work, but they have problems:

- **They drift across Claude versions.** A skill that worked on Sonnet 4.5
  may behave differently on Opus 4.7 because the model's interpretation of
  the markdown changed. The orchestration logic is *implicit in prose* and
  depends on the model executing it the same way every time.
- **They're agent-shaped, not graph-shaped.** A skill is a prompt; it can
  describe a workflow but it can't enforce one. There's no engine
  asserting "step 3 must complete before step 4"; the model decides.
- **They have no reactivity.** A skill can't say "if the validator at
  step 5 fails, re-run step 2 with the new inputs." The author has to
  encode that as a conditional branch in the prose and hope the model
  follows it.
- **They have no state-of-truth.** What ran? What committed? What
  invalidated what? The transcript is the only record, and it's not
  queryable.
- **They have no concurrency primitives.** Two parallel Claude sessions
  invoking the same skill can step on each other; there's no lock to
  coordinate.
- **They embed orchestration inside the model.** The same logic that
  should be a deterministic state machine ends up as a probability
  distribution over outputs.

A Rimsky template solves all six. The orchestration is in YAML/JSON and
versioned; nodes execute via the `claude-agent` executor (or `http-node`
for non-LLM steps); state lives in Postgres; locks are first-class;
reactivity is engine-derived.

Concrete local-dev scenarios where this beats writing a skill:

- **Document-maintenance workflows.** "When `feature-index.md` changes,
  invalidate the per-feature `cold-read` annotations; when annotations are
  re-derived, invalidate the architecture diagram; when the diagram
  changes, invalidate the README's quickstart section." A skill is a
  recipe; a Rimsky template is a graph that *enforces* the cascade. Each
  node is a `claude-agent` invocation with a tight prompt, but the
  *control flow* is in the orchestrator, not the model.
- **Multi-step refactor workflows.** "Identify call sites of function X;
  for each, propose a fix; run tests on each fix in isolation; on red,
  invalidate the proposal node and re-derive with the failure as
  constraint." Reactivity is the point — you want red tests to *cause*
  re-proposal, not to halt the workflow for human intervention.
- **Spec-implementation cascades.** "When a spec doc changes, invalidate
  the implementation plan; when the plan changes, invalidate the test
  scaffolding; when scaffolding changes, invalidate the implementation;
  when implementation passes, invalidate the changelog entry." Today this
  is multiple skills the human chains together by hand; in Rimsky it's one
  template instance the human invalidates at the root.
- **Codebase-audit workflows that resume.** A multi-hour audit pass over
  a large codebase that survives laptop sleep, can be paused/resumed, and
  reports incremental progress in `rimsky_events`. A skill is one Claude
  session — it dies when the context window fills or the laptop sleeps;
  Rimsky breaks it into nodes that each fit in their own context.
- **Local Claude Code agent fleets with shared state.** A `lint-fixer`
  node and a `test-runner` node both want exclusive access to the
  working tree; a region claim on the workdir serializes them. Today
  this is "open two terminals and remember not to run them at the same
  time."
- **Reproducible dev environments.** The template is content-addressed;
  re-registering the same spec is a cheap no-op. Sharing a dev workflow
  across a team becomes "deploy template `sha256-...`" rather than
  "copy this skill into your `~/.claude/skills/` directory and hope your
  Claude version interprets it the same way."

The framing for this audience: **a skill is a prompt; a Rimsky template is
a contract**. Skills are right when the workflow is genuinely
exploratory and you want the model to choose the next step. Rimsky is
right when you've done the workflow enough times to know the shape and
you want it to execute the same way regardless of which Claude version
you're on, regardless of whether you're at your desk or asleep,
regardless of whether someone else on the team kicks it off.

The deployment story makes this practical: `docker compose up` brings the
whole stack up locally. The control API is a localhost HTTP endpoint. A
project's `.claude/` directory can ship a `rimsky.yml` and a couple of
templates alongside its skills, and the slash commands invoking them are
one-line `curl` calls to `localhost:8080`. No cloud account, no SaaS
signup, no vendor commitment. The same template can run unchanged in CI,
on a teammate's laptop, or on a shared staging box.

This is also where the "Docker for agentic workflows" analogy lands
hardest. Docker won the local-dev environment because the abstraction was
small enough to learn in an afternoon and the runtime was free to run
anywhere. A Rimsky template's surface (nodes, claims, locks, attributes,
inheritance) is a similar size of vocabulary, and the runtime has the same
"runs on my laptop, in CI, in prod" property. If the project picks up
adoption, this is plausibly where it starts — not in production
multi-tenant SaaS deployments, but in `.claude/` directories on
individual developers' machines.

### 10.4 Where Rimsky is the wrong call

For symmetry. If your workflow is:

- **A scheduled batch job over partitioned data with stable graph shape**
  → Dagster or Airflow. The asset/lineage UI is the actual product, and
  Rimsky doesn't have one.
- **A single-process Python LLM agent with cyclic reasoning** → LangGraph
  in-process. The Rimsky overhead (Postgres, three processes, gRPC to
  executors) is unjustified for a workflow that fits in one process.
- **A long-running saga across microservices in a polyglot enterprise
  with strict at-least-once semantics and audit requirements that
  Cassandra can satisfy** → Temporal. Its event-history model and
  per-language SDK ergonomics are more mature than Rimsky's executor
  protocol for that specific shape.
- **A throwaway prototype that doesn't need to survive process restart**
  → don't bother with any orchestrator. A bash script or a single Python
  file is right.

---

## Sources

Compiled from web research per project; full citations inline above. Key
references:

- Dagster: [pricing](https://dagster.io/pricing),
  [open-core post](https://dagster.io/blog/open-core-business-model-dagster),
  [OSS-vs-Plus discussion](https://github.com/dagster-io/dagster/discussions/25313),
  [Pipes blog](https://dagster.io/blog/dagster-pipes).
- Prefect: [OSS comparison](https://www.prefect.io/compare/prefect-oss),
  [hidden costs of Airflow](https://www.prefect.io/blog/hidden-costs-apache-airflow).
- Airflow: [Shopify scale lessons](https://shopify.engineering/lessons-learned-apache-airflow-scale),
  [Asset scheduling](https://airflow.apache.org/docs/apache-airflow/stable/authoring-and-scheduling/asset-scheduling.html).
- Kestra: [OSS-vs-paid](https://kestra.io/docs/oss-vs-paid),
  [declarative-from-day-one](https://kestra.io/blogs/declarative-from-day-one).
- Temporal: [persistence](https://docs.temporal.io/temporal-service/persistence),
  [workflow execution limits](https://docs.temporal.io/workflow-execution/limits),
  [Cloud limits](https://docs.temporal.io/cloud/limits),
  [dynamic AI agents post](https://temporal.io/blog/of-course-you-can-build-dynamic-ai-agents-with-temporal).
- Restate: [first-principles post](https://www.restate.dev/blog/building-a-modern-durable-execution-engine-from-first-principles),
  [Cloud announcement](https://www.restate.dev/blog/announcing-restate-cloud-public).
- Inngest: [pricing](https://www.inngest.com/pricing),
  [usage limits](https://www.inngest.com/docs/usage-limits/inngest),
  [cross-language SDKs](https://www.inngest.com/blog/cross-language-support-with-new-sdks).
- LangChain / LangGraph: [LangGraph Platform / LangSmith Deployment](https://www.langchain.com/langgraph-platform),
  [Pricing](https://www.langchain.com/pricing),
  [persistence docs](https://docs.langchain.com/oss/python/langgraph/persistence),
  [RemoteGraph](https://docs.langchain.com/langsmith/use-remote-graph),
  [Aegra](https://www.aegra.dev/),
  [HN sentiment](https://news.ycombinator.com/item?id=44840323),
  [HN: why we no longer use LangChain](https://news.ycombinator.com/item?id=40739982).
- Microsoft Agent Framework: [Overview](https://learn.microsoft.com/en-us/agent-framework/overview/),
  [Workflows](https://learn.microsoft.com/en-us/agent-framework/workflows/),
  [v1.0 announcement](https://devblogs.microsoft.com/agent-framework/microsoft-agent-framework-version-1-0/),
  [VentureBeat on AutoGen retirement](https://venturebeat.com/ai/microsoft-retires-autogen-and-debuts-agent-framework-to-unify-and-govern).
- CrewAI: [pricing](https://crewai.com/pricing),
  [ZenML breakdown](https://www.zenml.io/blog/crewai-pricing).
- Flue: [flueframework.com](https://flueframework.com/),
  [withastro/flue GitHub](https://github.com/withastro/flue),
  [HN launch](https://news.ycombinator.com/item?id=47988501),
  [Astro Weekly #123](https://newsletter.astroweekly.dev/p/astro-weekly-123).
- Cross-cutting: [Kinde long-running agent patterns](https://www.kinde.com/learn/ai-for-software-engineering/ai-devops/orchestrating-multi-step-agents-temporal-dagster-langgraph-patterns-for-long-running-work/),
  [Render durable workflow platforms for AI agents](https://render.com/articles/durable-workflow-platforms-ai-agents-llm-workloads),
  [ZenML LangGraph alternatives](https://www.zenml.io/blog/langgraph-alternatives),
  [Akka Temporal alternatives](https://akka.io/blog/temporal-alternatives).
