# Build a Gas Town in Rimsky

A reference walkthrough for an opinionated multi-agent coding factory
deployed on Rimsky. Local for one developer, persistent on a single
box for a small team, scaled on a Kubernetes cluster for an
organization — same templates, three deployment topologies.

The example takes Steve Yegge's "dark factory" thesis seriously —
multi-agent coding teams that catch each other's mistakes, not single
agents managing infrastructure — and asks: *what does this look like
when you put a reactive control plane underneath it instead of a
prescribed topology around it?*

## Status

Marquee opinionated example. Concept fully fleshed; reference
implementation is the next concrete deliverable after
`bug-fix-from-tickets.md` lands (which this example builds on).

## What we're borrowing from Yegge, and what we're not

**Borrowed concepts:**

- **Dark factory.** "Any system in which coding agents are set up to
  work autonomously without humans watching." This example is a dark
  factory. The walkthrough deploys one.
- **Light Factory.** Yegge's framing: dark doesn't mean opaque. "The
  lights are on" — every worker addressable, every decision auditable,
  every artifact attributable. Rimsky's `rimsky_events`, content-
  addressed templates, and held-claim ledger make this property
  platform-enforced rather than convention.
- **Adversarial review.** "You should never just have one coding agent
  managing a piece of infrastructure." Multi-agent teams catch each
  other's mistakes through adversarial structure. This example
  implements that as a cascade with reverse invalidation from
  reviewer agents back to the implementer.
- **Multi-agent topologies.** Yegge's Packs are declarative
  composable building blocks for agent topologies. Rimsky templates
  serve the same role with different vocabulary.

**Not borrowed:**

- Yegge's specific component stack (Packs / MEOW / Beads / Dolt /
  Factory Worker API). Rimsky has its own primitives (cascade /
  locks / held claims / claim producers / executors / lifecycle
  subscribers); the example uses those.
- The TS/JS-leaning runtime assumption. Rimsky's executor protocol is
  polyglot — a Go reviewer and a Python static analyzer are first-
  class peers, not subprocess shellouts.
- The crypto/economy angle visible in some Gas Town discussion. Out
  of scope for this example.

The example's value is in showing that Yegge's thesis lands on a
different set of architectural choices than Gas City makes — not in
replacing Gas City. If you want Yegge's exact opinions about how a
factory should be structured, use Gas City. If you want a reactive
control plane that lets you build *your* opinionated factory, use
Rimsky and start from this example.

References to Yegge's writing are at the bottom.

## The opinionated topology

This example doesn't try to be the universal dark-factory template. It
picks one shape and demonstrates it concretely. Other topologies are
other examples or other templates against the same primitives.

### Roles (the agents and tools)

| Role | Implementation | What it does |
|---|---|---|
| **Planner** | `claude-agent` executor with planning prompt | Reads a ticket or task description; produces a structured plan: files to touch, approach, expected tests, risks. |
| **Implementer** | `claude-agent` executor with code-writing prompt | Reads the plan; writes the code changes on a feature branch. |
| **Nitpicker reviewer** | `claude-agent` executor with strict-style prompt | Reviews the implementer's diff for style, naming, dead code, comment quality, test coverage. Adversarial — looks for things to flag. |
| **Architect reviewer** | `claude-agent` executor with architectural-review prompt | Reviews the implementer's diff against the broader codebase: does this introduce coupling? Does it violate any blessed invariants? Does it duplicate logic? |
| **Test runner** | `http-node` executor wrapping the repo's test command | Runs the test suite against the feature branch. |
| **Deploy-gate** | `http-node` executor wrapping deploy validation | (Optional) For factories that auto-deploy, this gates merge on staging-environment health checks. |
| **Human reviewer** | Long-running async executor | Optional final sign-off. Configurable: required for some templates, skipped for others. |

### The cascade

```
ticket ──→ plan ──→ implement ──→ tests ──→ nitpick-review ──→ architect-review ──→ human-review (optional) ──→ merge
                                     │            │                    │
                                     ▼            ▼                    ▼
                                   (red)       (issues)             (issues)
                                     │            │                    │
                                     └─→ invalidate(implement) ←──────┘
                                              │
                                              ▼
                                          (3 rounds max)
                                              │
                                              ▼
                                       human-escalation
```

The reverse-cascade edges are the load-bearing structure. Three
distinct things can fire `invalidate(['implement'])`:

- The test runner finds a red test.
- The nitpicker finds a code-quality issue it considers blocking.
- The architect finds a structural problem.

After three rounds of cascade-rework, the budget exhausts and the
template routes to `human-escalation`. This is the "consensus through
adversarial structure" Yegge describes — implemented as engine
primitives, not as a prompt the model has to follow.

For higher-stakes work (e.g. anything touching prod infra), a second
template wraps this one with an additional `architect-review` pass at
the *plan* stage, which can `invalidate(['plan'])` before any code is
written.

## Primitives exercised

### Per-workspace, per-file, per-branch locks

Multi-agent fleets stepping on each other's edits is *the* failure
mode that motivates Yegge's whole framing. The Gas City answer is
careful topology design plus the Beads issue tracker. The Rimsky
answer is platform-enforced single-writer-per-file:

```yaml
locks:
  # One implementer at a time per branch
  - kind: scope
    scope:
      branch: "${plan.branch_name}"
    mode: serial
    held_by: [implement, nitpick-review, architect-review, tests, merge]

  # One writer at a time per file (in case multiple cascades target
  # the same file in different branches)
  - kind: scope
    scope:
      file: "${each_touched_file}"
    mode: serial
    held_by: [implement]
```

The git/filesystem claim producer enforces the scope on its end — it
refuses to write to a file unless the writing process holds the
matching claim address. The lock isn't advisory; it's a platform
invariant. Yegge's worry that "agents step on each other" is solved at
a different layer than Gas City solves it.

### Held claims across the multi-agent window

The branch claim is opened when `plan` decides on a branch name and
held across `implement`, the reviewers, `tests`, and `merge`. The
auto-terminal mechanism (`@blessed-invariant 13`) commits or abandons
based on the aggregate outcome of the holding subgraph:

- All-completed (tests green, both reviewers approve, human approves)
  → `Commit` → push branch, open and merge PR, close ticket.
- Any-failed → `Abandon` → delete branch, post failure log, escalate
  to human queue.

There's no "leftover branches the agents forgot to clean up" failure
mode. The claim producer owns the lifecycle.

### Named lock for the API budget

Multi-agent factories burn LLM tokens fast. A counting named lock
caps concurrent agent invocations:

```yaml
named_locks:
  - name: "anthropic-budget"
    mode: counting
    limit: 8        # at most 8 concurrent Claude agent invocations
  - name: "openai-budget"
    mode: counting
    limit: 4
```

This is the "per-tenant API budget" lock-primitive example (`README.md`
§2.3) applied to your own LLM provider account.

### Cascade with reverse invalidation

Already described above. The three reviewer-driven and one tests-driven
`invalidate` policies are the engine-derived alternative to "the
implementer agent re-prompts itself with the reviewer's complaints." In
the prompt-driven version, you're hoping the model follows instructions
and incorporates feedback; in the cascade version, the platform
guarantees the implementer node re-runs with the reviewer's output as
input.

### Polyglot peers

Each agent role is a separate process. Languages are independent:

- Planner / implementer / reviewers — TypeScript via `claude-agent`.
- Test runner — Go via `http-node`, shelling out to whatever the repo
  uses (`go test`, `npm test`, `pytest`, `cargo test`).
- Deploy gate — could be a Python script that checks staging metrics.
- Git/branch lifecycle — the reference filesystem claim producer (Go)
  with a small extension for git operations.
- GitHub lifecycle subscriber — Go peer that handles webhook delivery
  and PR/merge calls.

The factory isn't constrained to one language. Adding a Rust-based
static analyzer as a fourth reviewer is "register a peer that speaks
the executor protocol on `:8090`," not "fork the framework."

### Auditability — Yegge's "Light Factory" property

Every node transition lands in `rimsky_events`. The held-claim ledger
shows which agent process held the branch claim at any given timestamp
(claimant guard prevents stale orphans from impersonating a live
holder; `@blessed-invariant 4`). Templates are content-addressed
(`sha256-...`), so "what spec was this factory running at the time of
the incident" is a deterministic question.

A small read-only HTTP endpoint on the control-api turns this into a
"factory floor visibility" UI: live nodes by state, recent
invalidations, current claim holders, recent commits and abandons.
Yegge's Light Factory framing implemented as a queryable system rather
than a UI convention.

## Three deployment topologies, same templates

This is where the Docker analogy from
`../2026-05-02-rimsky-vs-landscape.md` §7 lands hardest. The example
ships three deployment configurations.

### Laptop topology

`docker compose up` brings the whole stack — Postgres, the three
rimsky processes, all peers, all reference executors — onto a single
host with the SQLite or local-Postgres driver. One developer's
factory; the templates are the same the team uses on the cluster.

```sh
cd examples/build-a-gas-town
docker compose up -d
curl -X POST localhost:8080/v1/instances \
    -d '{"template":"gas-town/coding-factory:latest","params":{...}}'
```

A skill in the user's `~/.claude/skills/start-factory.md` is one
`curl` call. Local Claude Code can dispatch tickets into the factory
running on the same laptop.

### Single-box persistent topology

The unified `rimsky/all` image. One Postgres, one PID-1 entrypoint
running the three rimsky processes, all peers as sidecar containers.
For a team of 3-10 sharing a factory on a small VM. Same templates;
the deployment is `docker compose -f deploy/rimsky-all.yml up -d` plus
a config swap.

### Cluster topology

The Helm chart at `deploy/kubernetes/rimsky-chart/`. Postgres as a
StatefulSet, the three rimsky processes as separate Deployments, peers
as their own Deployments behind ClusterIP services. Replicas scale
independently. For an organization-scale factory; same templates as
laptop and single-box.

The point of demonstrating all three: the templates don't know about
the deployment. A factory developed on a laptop runs unchanged on a
cluster. The opposite is also true — debug a cluster issue by
replicating the template on your laptop. This is the property Docker
established for processes; Rimsky aims for it for orchestrated
agentic workflows.

## Failure modes the example deliberately exhibits

A complete reference implementation should demonstrate end-to-end:

1. **Two simultaneous tickets touch the same file.** Per-file scope
   lock serializes the implements; second one waits, runs against the
   updated state.
2. **Implementer's first attempt fails tests.** Policy invalidates
   `implement`; second attempt sees the failing test as constraint.
3. **Nitpicker flags style issue.** Same — invalidate `implement`,
   re-run with the nitpick as constraint.
4. **Architect flags structural problem.** Same.
5. **Three rounds of rework, still failing.** Budget exhausts;
   `human-escalation` fires with full event log.
6. **Implementer process crashes mid-write.** Orphan reaper recovers
   the worker-request; held branch claim survives until auto-terminal
   resolves it. The branch is in a partial state; auto-terminal
   abandons (because the active node failed); the branch deletes.
7. **Reviewer agent disagrees with itself across runs.** This is the
   Gas City pathology Yegge calls out. Mitigation: low temperature for
   reviewers; prompt forces structured-output rubric scores; cascade
   logs the rubric so flapping is visible. Not a fix — but a
   *visible* failure mode rather than a silent one.
8. **One LLM provider goes down.** The `anthropic-budget` named lock
   is held by the cascade nodes; provider failure surfaces as
   per-node `infra_failure` errors with a `retry` action; budget is
   shared across templates so the factory degrades rather than
   thrashes.
9. **Two factories on the same repo.** Per-branch and per-file locks
   serialize across factories. (This is the Wasteland scenario —
   multiple factories sharing infrastructure; Rimsky's locks make
   this safe at the substrate level.)

## What this example does *not* claim to do

- It is not the universal dark-factory template. It picks one
  topology (planner + implementer + two reviewers + tests + optional
  human). Other topologies are different templates.
- It is not a Gas City or Gas Town competitor in the sense of
  reproducing Yegge's exact stack. It's an alternative architectural
  answer to the same thesis.
- It does not solve the LLM-quality problem. Adversarial review
  improves consensus but doesn't make the underlying agents smarter.
  The example *makes failure modes visible* (by structuring the
  cascade and logging every transition); making them rare is still
  the model and prompt-engineering team's job.
- It is not safe to run on a production codebase without supervision.
  The default templates ship with the human-review node *enabled*. A
  variant with `human-review` skipped is one config flag away, but
  the default opinion is "humans stay in the loop until the team
  has explicit confidence in the factory's outputs for a given
  problem class."
- It does not address the criticism that LLM-driven coding produces
  low-quality output regardless of orchestration. Rimsky-as-substrate
  is orthogonal to model quality. If the underlying agents are bad,
  the factory produces bad code faster.

The skeptical take in [Pivot to AI](https://pivot-to-ai.com/2026/01/22/steve-yegges-gas-town-vibe-coding-goes-crypto-scam/)
is worth reading in full and addressing directly in the example's
README — the criticisms about output quality, hallucination, and
"this is automating the production of slop" apply here too. The
honest answer: the cascade structure surfaces those failures earlier
and more visibly than a one-shot agent does, but it does not prevent
them.

## Reference implementation outline

```
examples/
  build-a-gas-town/
    rimsky.yml                  # one config; deploy variants below differ
                                # only in persistence / replicas
    rimsky-cluster.yml          # k8s overrides
    rimsky-all.yml              # single-box overrides
    docker-compose.yml          # laptop topology
    docker-compose.cluster.yml  # cluster reference (uses rimsky-chart)
    templates/
      coding-factory.yaml       # the headline cascade
      coding-factory-fast.yaml  # variant: nitpicker only, no architect, no human
      coding-factory-strict.yaml # variant: extra plan-stage architect review
    peers/
      planner-agent/            # claude-agent with planning prompt
      implementer-agent/        # claude-agent with implementation prompt
      nitpicker-agent/          # claude-agent with style-review prompt
      architect-agent/          # claude-agent with architectural prompt
      test-runner/              # http-node wrapping repo test commands
      git-lifecycle/            # branch/PR/merge lifecycle subscriber
      git-claim-producer/       # branch lifecycle as a claim producer
    skills/
      .claude/start-factory.md  # local-dev shortcut for the laptop topology
    README.md                   # quickstart + the three deployment paths
    LIMITATIONS.md              # honest take on what this does not solve
```

## What's next

Build order:

1. **Land the bug-fix-from-tickets example first.** It exercises the
   single-agent shape of every primitive this factory uses. Most of
   the peers (`claude-agent`, `http-node`, `git-claim-producer`,
   `git-lifecycle`) are shared.
2. **Add the multi-agent reviewer cascade.** This is the genuine
   addition Gas Town brings: parallel reviewers, reverse cascade from
   each, budget-bounded retry.
3. **Land the three deployment configurations.** Laptop and
   single-box are mostly config; cluster requires verifying the Helm
   chart against this concrete example.
4. **Write `LIMITATIONS.md`.** Address the skeptical take honestly.
   Document what failure modes the example surfaces, what failure
   modes it doesn't fix, what assumptions it makes about the
   underlying models.

## References

Yegge's primary writing on Gas Town and Gas City:

- [Welcome to Gas City](https://steve-yegge.medium.com/welcome-to-gas-city-57f564bb3607) — the SDK pitch
- [Welcome to Gas Town](https://steve-yegge.medium.com/welcome-to-gas-town-4f25ee16dd04) — the original Jan 2026 launch
- [Gas Town: from Clown Show to v1.0](https://steve-yegge.medium.com/gas-town-from-clown-show-to-v1-0-c239d9a407ec) — what changed and why
- [Welcome to the Wasteland: A Thousand Gas Towns](https://steve-yegge.medium.com/welcome-to-the-wasteland-a-thousand-gas-towns-a5eb9bc8dc1f) — the trust-network framing
- [The Future of Coding Agents](https://steve-yegge.medium.com/the-future-of-coding-agents-e9451a84207c)

Discussion and critique:

- [SE Daily podcast: Gas Town, Beads, and the Rise of Agentic Development](https://softwareengineeringdaily.com/2026/02/12/gas-town-beads-and-the-rise-of-agentic-development-with-steve-yegge/)
- [Pivot to AI's skeptical take](https://pivot-to-ai.com/2026/01/22/steve-yegges-gas-town-vibe-coding-goes-crypto-scam/) — read the criticisms; the example's `LIMITATIONS.md` should address them by name
- [DoltHub: A Day in Gas Town](https://www.dolthub.com/blog/2026-01-15-a-day-in-gas-town/)
- [Gas Town Hall](https://gastownhall.ai/) — community

Underlying Rimsky primitives:

- `../architecture.md` — cascade, claims, frames
- `../specs/2026-05-04-foundation-contract.md` — atomic acquisition, auto-terminal
- `../2026-05-02-rimsky-vs-landscape.md` §10.3 — "skill is a recipe, template is a contract"
- `bug-fix-from-tickets.md` — single-agent precursor exercising the same primitives
