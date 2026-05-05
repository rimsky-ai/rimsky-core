# Rimsky examples

A catalog of example templates and reference apps that demonstrate where
Rimsky earns its architecture. Each entry names the problem, the
primitives it exercises, and what makes it hard to do well today without
Rimsky.

The catalog is grouped into three families. The order reflects what we
think is the right *demonstrative* order — not necessarily the order
they'll be built.

1. **Agent workflow examples.** The headline category. Multi-step,
   stateful, externally-mutating work driven by LLM agents and human
   reviewers. This is the "underrated case" identified in
   `../2026-05-02-rimsky-vs-landscape.md` §10.3 — and on reflection, it's
   probably Rimsky's natural home rather than data-pipeline orchestration.
2. **Lock-primitive examples.** Small, focused demonstrations of the
   per-key dynamic lock primitive without the full cascade machinery.
   Each one is a problem teams hit in practice and currently solve with
   ad-hoc Redis or Postgres advisory locks.
3. **Reactive cascade examples.** Demonstrations of engine-derived
   `invalidate(targets)` — the load-bearing differentiator from forward-
   only orchestrators.

A reference example lands when:
- A complete `rimsky.yml` and a complete template (or templates) live
  under `examples/<name>/`.
- A reference walkthrough lives at `docs/examples/<name>.md` describing
  the cascade shape, what each node does, what each lock guards, what
  failure modes the example exhibits, and what the "do-it-without-rimsky"
  baseline looks like.
- The walkthrough builds and runs against `deploy/docker-compose.yml`
  with the included reference executors and producers.

---

## Why agent workflows, not data pipelines

Restating the case from earlier strategy work, condensed:

| Property | Data pipeline | Agent workflow |
|---|---|---|
| Unit of work | Transform input → output | Multi-step interaction with shared external state |
| Output location | New artifact (table, file) | Mutation of pre-existing state (repo, ticket, draft) |
| Concurrency conflicts | Rare | Common (two agents editing the same file/PR/ticket) |
| Failure recovery | Re-run transform; idempotent | Partial work is in the world; needs commit/abandon |
| Tool heterogeneity | Mostly one runtime | Claude + test runner + git + GitHub + CI + deploy |
| Held resources across steps | Rare | Pervasive (branches, sandboxes, drafts) |

Every row on the right maps to a Rimsky primitive that's load-bearing.
Locks, held claims, claim-producer commit/abandon, polyglot peers — the
things barely-exercised by data-pipeline workloads are *necessary* for
orchestrating agents safely.

The competitive picture (drawn from `../2026-05-02-rimsky-vs-landscape.md`):

- **Temporal / Restate / DBOS** — durable execution, no per-key locks, no
  reactive cascade, no claim-producer protocol.
- **LangGraph / LangChain / CrewAI** — agent-first frameworks, in-process,
  no platform-level locks, no multi-tenancy, no cross-thread cascade.
- **Flue (Astro, Nov 2026)** — single-agent harness; explicitly not
  multi-agent orchestration. Composes *under* Rimsky as an executor.
- **Steve Yegge's Gas Town / Gas City** — the closest thing to a stated
  target shape: "dark factories" of coordinated coding agents. Gas City
  is Yegge's SDK answer (Packs / MEOW / Beads / Dolt / Factory Worker
  API). Rimsky is a different shape of answer to the same question — a
  reactive control plane *underneath* a dark factory rather than a
  prescribed topology *for* one. ([Gas City announcement](https://steve-yegge.medium.com/welcome-to-gas-city-57f564bb3607);
  [Gas Town origin](https://steve-yegge.medium.com/welcome-to-gas-town-4f25ee16dd04);
  [The Wasteland](https://steve-yegge.medium.com/welcome-to-the-wasteland-a-thousand-gas-towns-a5eb9bc8dc1f))

Nobody owns "reactive control plane for agent workflows that mutate
shared external state." The space is open in the way "Dagster owns
asset-based data pipelines" is closed.

---

## 1. Agent workflow examples

### 1.1 Bug-fix from user tickets

**Problem.** A user files a bug ticket. An agent triages it, reproduces
the bug, writes a fix on a branch, runs tests, opens a PR, waits for
review, merges on approval. If anything fails, escalate to a human.

**Why it's a Rimsky shape.**

- *Cascade:* `ticket → triage → reproduce → fix-attempt → tests → PR →
  review → merge`. New comment on the ticket invalidates `triage` and
  the cascade re-fires from the right node.
- *Per-ticket lock:* prevents two agent invocations from processing the
  same ticket in parallel.
- *Per-repo or per-directory held claim:* prevents conflicting
  concurrent fixes; the held claim outlives a single node and spans the
  fix-attempt → tests → PR window.
- *Producer commit/abandon:* on success, merge the PR and close the
  ticket; on failure, delete the branch, comment with what failed,
  escalate to a human queue.
- *Polyglot peers:* Claude does triage and fix; the test runner is
  whatever the repo uses; GitHub is the lifecycle peer.

**What the do-it-without-Rimsky baseline looks like.** Temporal +
custom GitHub bot + a hand-rolled lock layer (Redis or Postgres
advisory) + bespoke escalation logic + a sensor for "ticket got new
info." All of those pieces have to be wired together by hand and the
inter-piece state is a constant source of bugs.

**Detailed sketch:** `bug-fix-from-tickets.md` (this directory).

**Status.** Sketched in walkthrough form; reference implementation TBD.

---

### 1.2 Code review automation with multi-agent checks

**Problem.** A PR is opened. Multiple checks run in parallel: spec
compliance, test coverage, security scan, dependency audit, style
review. Each check is an agent (or a deterministic tool, or a hybrid).
Some checks ask the author questions and wait for replies. New commits
pushed mid-review re-trigger only the affected checks.

**Why it's a Rimsky shape.**

- *Cascade:* per-check nodes hang off the PR root. New commit pushed
  → invalidates only the nodes whose inputs actually changed (e.g. a
  README-only change doesn't re-fire the test-coverage check).
- *Per-PR lock:* commits arriving faster than checks complete don't
  produce N parallel runs of the same check.
- *Held claim across multi-turn dialogue:* "agent asks the author a
  question, waits, resumes" maps directly onto the held-claim model —
  the claim outlives the active phase of the agent's run.
- *Polyglot peers:* spec compliance might be a Go service, security
  scan might be a Python tool, style review might be a Claude agent.

**What the do-it-without-Rimsky baseline looks like.** GitHub Actions
+ a stack of bot integrations + custom rerun-only-affected-checks logic
+ no shared state across checks. Re-running the whole CI suite on every
commit is the ergonomic default; selective re-run is a constant
afterthought.

**Status.** Concept only.

---

### 1.3 Customer-support response drafting with human-in-the-loop

**Problem.** Inbound support email arrives. Cascade: parse → categorize
→ search knowledge base → draft response → tone/policy check → human
review queue → send. Customer follow-up email mid-flow re-fires the
cascade from "categorize" forward.

**Why it's a Rimsky shape.**

- *Per-conversation lock.* Don't draft two responses in parallel for
  the same thread.
- *Held claim across multi-step drafting.* The agent's draft state,
  intermediate KB lookups, candidate responses all live in a held claim
  that's committed when the human approves and sends, abandoned if the
  human rejects.
- *Reactive cascade.* Customer's follow-up email is a downstream event
  that invalidates upstream nodes ("categorize") — exactly the
  reverse-cascade shape that forward-only orchestrators can't model
  without a custom sensor.
- *Multi-protocol coordination.* Email provider + KB + LLM + ticket
  system + outbound mailer are all peers.

**What the do-it-without-Rimsky baseline looks like.** Zendesk +
custom integrations + Lambda + ad-hoc drafting state in DynamoDB +
race conditions when two agents pick up the same conversation.

**Status.** Concept only.

---

### 1.4 Build a Gas Town in Rimsky

**Problem.** Steve Yegge's "dark factory" thesis: stop running one Claude
Code instance at a time; deploy *teams* of coordinated coding agents
that work autonomously, catch each other's mistakes, and produce more
reliable consensus through adversarial structure. Gas City is Yegge's
opinionated SDK (Packs, MEOW, Beads, Dolt, Factory Worker API).

This example is the opinionated counter-pitch: *here's what a dark
factory looks like when you put a reactive control plane underneath it
instead of a prescribed topology around it.* Same problem (deploy a
multi-agent coding factory), different shape of answer.

**Why it's a Rimsky shape.**

- *Cascade for adversarial review.* Yegge's "never one agent, always a
  team" maps onto a cascade where a primary agent's output is
  invalidated by reviewer agents that find problems — the cascade
  re-fires the primary with the reviewer's complaints as constraints.
  Engine-derived `invalidate(targets)` does this in one policy.
- *Per-workspace and per-file locks.* Multi-agent fleets stepping on
  each other's edits is the failure mode that motivates Yegge's whole
  framing. Per-file scope locks plus a workspace claim producer
  enforce single-writer-per-file as a *platform invariant*, not as
  a convention everybody hopes the agents follow.
- *Held claims across multi-step agent work.* Branch claimed at start
  of a refactor, held across plan → implement → test → review →
  commit-or-abandon. The producer commits the branch (push + PR) on
  success and abandons it (delete) on failure. No partial-work
  pollution.
- *Polyglot peers.* Yegge's Gas City is TS/JS-leaning; Rimsky's
  executor protocol means a Go reviewer agent, a Python static
  analyzer, and a Claude planner can all be peers in the same
  factory without sharing a runtime.
- *Local OR persistent OR scaled deployment.* `docker compose up` for
  a laptop dark factory; the unified `rimsky/all` image for a single-
  box persistent deployment; the Helm chart for a cluster-scale one.
  Same templates, three deployment topologies — the Docker-analogy
  framing from `../2026-05-02-rimsky-vs-landscape.md` §7 in action.

**The opinionated topology.** This example doesn't try to be the
universal dark-factory template. It picks one shape and demonstrates
it concretely:

- **Roles.** Planner agent, implementer agent, two adversarial
  reviewer agents (one nitpicker, one architect-level), test-runner
  executor, deploy-gate executor.
- **Coordination.** Per-ticket cascade. Per-branch held claim. Per-file
  scope lock during implement and review. Named lock for "at most N
  concurrent agent invocations against the OpenAI/Anthropic API"
  (cost guardrail).
- **Failure handling.** Adversarial reviewers can `invalidate(['plan'])`
  or `invalidate(['implement'])`. Test failure invalidates `implement`.
  Three rounds of cascade-rework before escalating to a human queue.
- **Audit.** Every decision lands in `rimsky_events`; the held-claim
  ledger shows which agent had which file at which time. Yegge calls
  this a "Light Factory" — every worker visible and addressable. The
  producer-commit semantics make that property platform-enforced.

**Status.** Marquee example. Concept fully fleshed; reference
implementation is the next concrete deliverable. See
`build-a-gas-town.md` (this directory) for the longer sketch.

References:
- [Welcome to Gas City](https://steve-yegge.medium.com/welcome-to-gas-city-57f564bb3607)
- [Welcome to Gas Town](https://steve-yegge.medium.com/welcome-to-gas-town-4f25ee16dd04)
- [Gas Town: from Clown Show to v1.0](https://steve-yegge.medium.com/gas-town-from-clown-show-to-v1-0-c239d9a407ec)
- [Welcome to the Wasteland: A Thousand Gas Towns](https://steve-yegge.medium.com/welcome-to-the-wasteland-a-thousand-gas-towns-a5eb9bc8dc1f)
- [Software Engineering Daily: Gas Town, Beads, and the Rise of Agentic Development](https://softwareengineeringdaily.com/2026/02/12/gas-town-beads-and-the-rise-of-agentic-development-with-steve-yegge/)
- [Pivot to AI's skeptical take](https://pivot-to-ai.com/2026/01/22/steve-yegges-gas-town-vibe-coding-goes-crypto-scam/) (worth reading for the criticisms the example should address head-on)

---

### 1.5 Document-maintenance cascade

**Problem.** A change to one document invalidates derived documents.
Concrete: when `feature-index.md` changes, the per-feature cold-read
annotations are stale; when annotations change, the architecture
diagram is stale; when the diagram changes, the README quickstart is
stale. Each derivation is a Claude agent invocation.

**Why it's a Rimsky shape.**

- *Engine-derived invalidation across documents.* This is the
  archetypal "skill is a recipe, template is a contract" example from
  `../2026-05-02-rimsky-vs-landscape.md` §10.3.
- *Content-addressed templates.* "Re-derive these docs the same way I
  did last month" is `deploy template sha256-...`, not "re-paste my
  skill into your `~/.claude/skills/`."
- *Per-doc lock.* Two parallel agents both writing to
  `architecture.md` would corrupt the file.

**What the do-it-without-Rimsky baseline looks like.** A skill that
prompts Claude to do all of it in one session. Works on one model
version, drifts on the next, has no enforcement, has no reactivity.

**Status.** Strong fit; concept only.

---

### 1.6 Multi-step refactor

**Problem.** "Identify call sites of function X; for each, propose a
fix; run tests on each fix; on red, invalidate the proposal node and
re-derive with the failure as constraint." Reactivity is the point —
red tests *cause* re-proposal, not human intervention.

**Why it's a Rimsky shape.** Engine-derived `invalidate(targets)` from
test failures is a one-line policy in Rimsky and a custom sensor +
backfill operator in any forward-only orchestrator. Per-call-site
held claim spans propose → fix → test → commit-or-abandon.

**Status.** Strong fit; concept only.

---

### 1.7 Spec-implementation cascade

**Problem.** Spec doc changes → implementation plan stale → test
scaffolding stale → implementation stale → changelog entry stale.
Currently this is multiple skills the human chains together by hand;
in Rimsky it's one template instance the human invalidates at the
root.

**Why it's a Rimsky shape.** Same as 1.5; this one is more code-shaped
and more clearly demonstrates the "templates as contracts" framing for
a developer audience.

**Status.** Strong fit; concept only.

---

### 1.8 Skillprompting player (for giggles)

**Problem.** [skillprompting.com](https://skillprompting.com/llms.txt) is
an AI-judged writing contest platform with crypto prizes (SOL on
Solana). Rounds post a topic with character limits and judging
criteria; entries pay a Solana transaction fee or solve an argon2id
proof-of-work puzzle for free entry; an AI judges and the pot pays
out. They explicitly mention AMOE postcard entry "for humans or
agents," so agent participation isn't adversarial to the platform.

A Skillprompting player is an autonomous agent that watches for new
rounds, decides whether to enter (criteria filter, ROI estimate),
generates a submission, picks an entry path (pay, puzzle, postcard),
submits, and tracks outcomes.

**Honest framing.** This example is included because it's *fun*, not
because it uniquely justifies any Rimsky primitive. A Temporal workflow
or a Python script with a state machine would do the job. What it does
demonstrate well:

- *Polyglot peers in a non-enterprise context.* Solana signing in Rust,
  argon2id puzzle solver tuned in C, LLM submission generator in TS, all
  as peers behind the executor protocol. Most polyglot examples are
  enterprise-shaped; this one is whimsical.
- *Reactive state machine for the round lifecycle.* Round state
  transitions (`open` → `closing` → `completed`) drive cascade
  invalidations cleanly.
- *Held claim around wallet operations.* Hold an ephemeral wallet
  across "generate → sign → submit → confirm or abandon"; the producer
  commits on confirmed tx, abandons (refunds / cancels) on failure.
- *Per-round serial lock.* One submission per round per identity.
- *Content-addressed strategy templates.* "I'm running v3 of my
  haiku-strategy template" is a first-class deployment statement.

**What this example does *not* claim to demonstrate.** Cost-effective
gambling, novel LLM techniques, or any guarantee of winning. Treat
entry budgets responsibly; check the platform's ToS before deploying;
the author is not your financial advisor.

**Status.** Sketched in walkthrough form. See
`build-a-skillprompting-player.md` (this directory).

---

### 1.9 "Build a 'Build an X in Rimsky' Example Generator in Rimsky"

**Problem.** This catalog needs more examples. Generating them by hand
is slow, opinion-bound, and biases toward what the author can think of
on a Tuesday. This example builds the example generator: a Rimsky
cascade that searches the web for problem domains, fans out parallel
idea generators with different framings, judges the candidates against
the existing catalog and against rimsky-fit criteria, scaffolds the
winner as a walkthrough draft, validates the proposed cascade YAML,
runs it through an adversarial reviewer, and on acceptance opens a PR
that adds the new example to `docs/examples/`.

The recursion is the joke. The demonstration value is real.

**Why it's a Rimsky shape.**

- *Multi-agent fan-out with adversarial judge.* The "Yegge adversarial
  review" pattern from `build-a-gas-town.md` applied to example
  *generation* itself. Multiple idea generators with different prompts
  (enterprise / whimsical / dev-tools / meta) propose candidates in
  parallel; a judge agent scores them.
- *Cascade reads its own output directory.* `docs/examples/` is an
  input to the deduplication node. The catalog is self-aware and
  refuses to propose redundant examples.
- *Reverse cascade from reviewer to scaffolder.* If the reviewer rejects
  the draft (low quality, factually wrong about rimsky primitives,
  redundant with an existing example the dedupe missed), policy fires
  `invalidate(['scaffold-example'])` with the critique as constraint.
  Three rounds; then escalate to the human catalog maintainer.
- *Held claim around the draft branch.* Scaffolder opens a feature
  branch claim, holds across tester and reviewer; commits (push + open
  PR) on accept; abandons (delete branch) on reject. Exactly the
  bug-fix-from-tickets pattern, applied recursively to rimsky's own
  documentation.
- *Polyglot peers.* Web search (Go), idea generators and judge and
  reviewer (TS / claude-agent), tester (Go — validates YAML, runs
  `rimsky-conformance` against the proposed cascade), publisher (Go).
- *Named lock for LLM budget.* Speculative example generation can burn
  tokens fast; a counting named lock caps concurrent agent invocations.

**The fun bits.**

- The cascade can propose examples that include *itself*. The dedupe
  node has to reject `"build a 'build a build a build a' generator"`
  recursion — or accept it as deliberate, depending on judge mood.
  The reference impl ships with a hard depth cap.
- A "meta" idea generator framing prompt explicitly looks for
  self-referential or reflexive examples. This is where the recursion
  gets weird in a productive way.
- The example catalog has a measurable growth rate. With cron-triggered
  invocations and a weekly budget, the catalog generates itself
  asymptotically.
- The example generator's *own walkthrough* — the file you're reading
  references — was hand-written. The first task of the deployed
  generator is generating a better version of itself, which is a
  legitimate cascade input.

**What this example does *not* claim to do.** Generate good examples
without human review; this is why the reviewer node and the human
escalation exist. Replace the catalog maintainer; the generator opens
PRs, it doesn't merge them. Bootstrap rimsky's example catalog from
zero; it requires the existing catalog as input, which is the
philosophical point.

**Status.** Sketched in walkthrough form. Reference implementation
deliberately gated behind the bug-fix and gas-town examples landing
first — they're the primitives this generator depends on, and shipping
them out of order would be funny but unhelpful. See
`example-generator.md` (this directory) for the longer sketch.

---

### 1.10 Resumable codebase audit

**Problem.** A multi-hour audit pass over a large codebase that
survives laptop sleep, can be paused and resumed, reports incremental
progress in a queryable event log, and breaks the work into nodes that
each fit in their own LLM context window.

**Why it's a Rimsky shape.**

- *Frame-bracketed progress.* One frame per audit run; nodes go
  `stale → running → fresh` as the audit progresses.
- *Survives process restart.* Postgres holds the truth; `docker compose
  down && up` mid-run resumes at the orphan-reap interval.
- *Per-node context.* A skill is one Claude session that dies when its
  context window fills; a Rimsky template is a graph of nodes that
  each get their own context.

**Status.** Strong fit; concept only.

---

## 2. Lock-primitive examples

These are deliberately small. Each one demonstrates the per-key
dynamic lock primitive on a problem that today is solved with ad-hoc
Redis Redlock or Postgres advisory locks. The point of having them as
separate examples is that *the lock primitive is independently useful*
even if the cascade and held-claim machinery is overkill for your
problem.

### 2.1 Web scraper per-host politeness

**Problem.** You scrape data from many sources. Each source's host has
rate limits or robots.txt-implied politeness windows. You want at most
one fetch in flight per host, with a minimum gap between fetches,
shared across all your concurrent extraction jobs.

**Primitive exercised.** Per-host scope lock with `mode: serial`. A
small claim producer enforces the inter-fetch gap on its end.

**Without Rimsky.** Per-host Redis lock + sleep loops, hand-rolled in
each scraper.

**Status.** Concept; small enough to be a good first reference impl.

---

### 2.2 Shared git workspace under multiple agents

**Problem.** Multiple automated processes (CI bots, dependabot, agent-
driven refactors, codegen) all making changes to one repo. Two agents
trying to edit the same file = lost work or merge hell. Two agents
trying to push to the same branch = clobbering.

**Primitive exercised.** Per-file or per-branch scope lock with the
filesystem/git claim producer. The producer enforces correctness on
its end (refuses to write if claim address mismatches).

**Without Rimsky.** Hand-rolled `.git/index.lock` checks, file lock
files, race conditions in CI, "open two terminals and remember not to
run them at the same time."

**Status.** Concept; this is actually a sub-problem of the Gas Town
example — landing this as a standalone example first is a sensible
build order.

---

### 2.3 Per-tenant third-party API budget

**Problem.** B2B SaaS where each customer has connected their own
Salesforce / Shopify / GitHub / Stripe. The third party rate-limits
per the *customer's* account. Many internal jobs hit those APIs on
each customer's behalf and need a per-account token bucket shared
across all internal services.

**Primitive exercised.** Per-account named lock in counting mode
(`limit: N`) plus a producer-managed queue.

**Without Rimsky.** Bespoke Redis-backed bucket per integration, per
service, often diverging across teams.

**Status.** Concept.

---

## 3. Reactive cascade examples

These demonstrate engine-derived `invalidate(targets)` — the load-
bearing differentiator from forward-only orchestrators. Each one is a
small focused template that does one thing.

### 3.1 Schema-drift pipeline

**Problem.** An ingestion node consumes a CSV; a downstream validator
notices the column set drifted; the policy fires
`invalidate(['fetch-schema-config'])` so the next cascade re-derives
the config from a fresh schema probe before re-ingesting.

**Primitive exercised.** Engine-derived `invalidate(targets)` going
*upstream* from a downstream observation.

**Without Rimsky.** Custom sensor + manual backfill + on-call alert
("schema changed, re-run with the right config"). Forward-only
orchestrators can't model this without out-of-band tooling.

**Status.** Adapted from `../node-graph-design.md` examples; should be
trivial to land as a reference impl.

---

### 3.2 Probe-driven pipeline

**Problem.** A `probe-ingestion` node runs a small-N test against the
full pipeline before a full run. Probe failure routes back to the
`prepare-config` node rather than retrying the probe with the same
bad config.

**Primitive exercised.** Same as 3.1; this is the
"probes-are-write-region-holding-nodes" pattern from
`../node-graph-design.md` §3.6.

**Status.** Concept; close cousin to 3.1.

---

### 3.3 Evaluator-revised LLM prompts

**Problem.** A judge node scores an agent's output against rubric
criteria. On `score_below_threshold`, invalidate the prompt-construction
node upstream so the next run rebuilds the system prompt with the
failure mode encoded as a constraint.

**Primitive exercised.** Reverse-cascade from a downstream judge to
an upstream prompt-builder. Forward-only orchestrators retry the
agent against the same prompt; Rimsky reaches back.

**Status.** Strong demonstrative value; concept only.

---

### 3.4 Config-validity drift

**Problem.** A SaaS integration's API key rotated mid-run. The
auth-failure error class invalidates the credential-fetch node rather
than retrying with the stale token.

**Primitive exercised.** Per-error-class `invalidate(targets)` policy.

**Status.** Adjacent to 3.1; could share a reference impl.

---

## Build order — recommended

1. **Lock-primitive examples (2.1–2.3) first.** Smallest scope; each is
   a one-template demonstrator. Land them as a set; they reinforce
   the lock primitive as independently useful.
2. **Reactive cascade examples (3.1, 3.3) next.** Two small templates
   that show engine-derived invalidation in pipeline and agent shapes
   respectively.
3. **Bug-fix from tickets (1.1).** First substantial agent-workflow
   example. Exercises every primitive. Maps onto a problem
   universally felt.
4. **Build a Gas Town (1.4).** The marquee opinionated example.
   Builds on the bug-fix example's primitives plus multi-agent
   coordination patterns. Lands with a full deployment story (laptop /
   single-box / cluster).
5. **Document-maintenance cascade (1.5)** and **Spec-implementation
   cascade (1.7).** These reinforce the "templates as contracts"
   framing for the developer-tools audience.
6. The remaining agent examples (1.2, 1.3, 1.6, 1.8) and reactive
   examples (3.2, 3.4) land as time and demand allow.

---

## Open questions for the catalog

- *Should each example ship its own minimal `rimsky.yml` or share a
  base config?* Probably the former — the deployment story is part of
  what each example demonstrates.
- *Should examples live under `examples/<name>/` at the repo root, with
  walkthroughs at `docs/examples/<name>.md`?* Provisionally yes; the
  walkthrough cross-links to the runnable code.
- *Should the Gas Town example deliberately depart from Yegge's
  vocabulary (Packs / MEOW / Beads) or borrow it?* Provisionally:
  borrow the *concepts* (Light Factory, adversarial review,
  multi-agent teams) and use Rimsky's vocabulary for the
  implementation. The example's value comes from showing the same
  thesis with a different architecture, not from co-opting Yegge's
  terms.
- *Does the catalog need a "non-examples" section listing the shapes
  Rimsky is the wrong tool for?* `../2026-05-02-rimsky-vs-landscape.md`
  §10.4 already covers this. A short pointer suffices.
