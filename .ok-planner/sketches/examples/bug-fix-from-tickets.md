# Bug-fix from user tickets

A reference walkthrough for an agent workflow that takes a user-filed
bug ticket and drives it through triage, reproduction, fix, test, PR,
review, and merge — entirely as a Rimsky cascade.

This is the headline agent-workflow example. It exercises every Rimsky
primitive: cascade with reverse invalidation, per-key dynamic locks,
held claims spanning multiple nodes, claim-producer commit/abandon
delegation, and polyglot peers.

## Status

Concept walkthrough. Reference implementation is the next concrete
deliverable.

## Why this example

A team running an automated bug-fix pipeline today is gluing together:

- A ticket-system webhook (Linear / GitHub Issues / Jira)
- A workflow engine (Temporal / GitHub Actions / Inngest) for the
  multi-step orchestration
- A bot or agent runtime (Claude Code, custom GH App) for the actual
  triage and fix
- A lock layer (Redis Redlock or Postgres advisory) to prevent two
  concurrent fixes on the same repo or ticket
- A failure handler that escalates to a human queue with the right
  context attached

The integration cost is high, the failure modes are inter-piece, and
the most common production bug is "two things ran when only one should
have." Rimsky models the whole flow as one cascade.

## The cascade

```
ticket-event ─┐
              ├─→ triage ──→ reproduce ──→ fix-attempt ──→ tests ──→ pr-open ──→ review ──→ merge
ticket-update ┘                                                                    │
                                                                                   ▼
                                                                              human-escalation
```

- `ticket-event` and `ticket-update` are external-invalidation entry
  points wired up by the GitHub/Linear lifecycle subscriber peer.
- `triage` reads the ticket, classifies it (`bug` / `feature-request` /
  `not-actionable`), extracts the relevant repo, files, and reproduction
  hints. Output: a structured triage record committed to the cascade.
- `reproduce` attempts to construct a failing test or repro script.
  If it can't reproduce, the policy fires `invalidate(['triage'])` with
  reason `cant_reproduce_with_current_info` — triage will re-run, this
  time looking harder or asking the user for more info.
- `fix-attempt` reads the triage + repro and writes a candidate fix on
  a feature branch.
- `tests` runs the repo's test suite against the fix branch. On red,
  policy fires `invalidate(['fix-attempt'])` with the failure as
  constraint.
- `pr-open` opens a PR, links the ticket, attaches the triage record
  and the test report.
- `review` is human-driven (the human is one of the executors here);
  the node sits in a long-running async state until the human approves
  or requests changes.
- `merge` commits the held claim's changes (push to main, close the
  ticket).
- `human-escalation` is the terminal node fired when retry budgets are
  exhausted. Posts to Slack with the cascade's full event log.

## Primitives exercised

### Cascade with reverse invalidation

The two reverse-cascade edges are the load-bearing differentiators:

- `reproduce` failing → `invalidate(['triage'])`
- `tests` failing → `invalidate(['fix-attempt'])`

In a forward-only orchestrator (Airflow, Dagster, Prefect, GitHub
Actions), these are out-of-band: a failing test halts the workflow,
the operator manually re-runs the fix step with new inputs. In Rimsky
they're one-line `error_types` policies:

```yaml
nodes:
  - id: tests
    executor: test-runner
    error_types:
      test_failure:
        action: invalidate
        targets: [fix-attempt]
        budget: 3        # cap retry budget; on exhaustion → give_up
      infra_failure:
        action: retry
        budget: 5
```

### Per-ticket lock

A scope lock keyed on `(repo, ticket-number)` ensures the cascade
runs at most once per ticket. The ticket-event peer is idempotent
against duplicate webhooks; the lock makes that idempotency a platform
guarantee, not a property the peer has to enforce internally.

```yaml
locks:
  - kind: scope
    scope:
      repo: "${ticket.repo}"
      ticket: "${ticket.number}"
    mode: serial
    held_by: [triage, reproduce, fix-attempt, tests, pr-open, review, merge]
```

### Held claim across the fix window

The fix branch is a held resource that spans multiple nodes. The
filesystem-or-git claim producer (a peer service) opens the claim at
`fix-attempt` start, holds it across `tests` and `pr-open`, and the
auto-terminal mechanism resolves it at the holding-subgraph close:

- All-completed → `Commit` → push to `main`, close the ticket.
- Any-failed → `Abandon` → delete the branch, post a comment with
  what failed, route to human queue.

This is `@blessed-invariant 13` (auto-terminal aggregate-outcome
resolution) doing real work.

### Per-repo named lock for cost guardrails

A counting named lock caps concurrent agent work on a single repo —
prevents the Claude budget from going through the floor when ten
tickets land at once for the same project.

```yaml
named_locks:
  - name: "agent-budget:${repo}"
    mode: counting
    limit: 3
```

### Polyglot peers

- `triage`, `reproduce`, `fix-attempt`, `review` (the agent prompt for
  format-checking the human's review) — `claude-agent` executor (TS).
- `tests` — the repo's existing test runner, wrapped by the `http-node`
  executor (Go).
- `pr-open`, `merge` — GitHub via a small lifecycle-subscriber peer.
- The fix branch lifecycle is owned by the git claim producer (a
  reference impl ships under `stores/filesystem` or a sibling
  `stores/git`).

No single-language assumption. Each peer is a separate process that
speaks the protocol; languages are independent.

## What the do-it-without-Rimsky baseline looks like

A representative implementation circa 2026:

- **Temporal workflow** orchestrating the steps. The reverse-cascade
  edges (`tests` failure → re-run `fix-attempt`) become explicit
  branching in the workflow code, with retry counters threaded through
  the workflow state.
- **Per-ticket idempotency** is the workflow's responsibility — usually
  a workflow ID derived from `(repo, ticket-number)` and a "deduplicate
  on conflict" policy. Works, but the orchestrator's primitive is
  *workflow execution*, not *ticket lock*; the abstraction is leaky
  whenever a follow-up workflow needs to be scoped to the same ticket.
- **Branch lifecycle** is a separate concern — the workflow has to
  remember to clean up branches on cancellation, on failure, on
  workflow-history-too-large abort. Easy to leak orphan branches.
- **Custom GitHub App** for ticket events, PR open, merge, comment.
  Webhook handler dispatches to Temporal start-workflow.
- **Slack escalation handler** is a separate Lambda or service.
- **The lock layer is ad-hoc.** "We use Redis Redlock for the
  per-ticket lock and per-repo budget cap" is the typical answer; the
  failure modes (Redlock liveness under partitions, lock TTL chosen
  badly, holder crashes) are well-understood but require operator
  vigilance.

The Rimsky version isn't smaller in terms of *services running* — there
are still peers for GitHub, the test runner, the agent, the git claim
producer. It's smaller in terms of *integration code*. The cascade,
the locks, the held claim, and the auto-terminal resolution are all
declarative; the orchestrator's vocabulary covers them directly.

## Failure modes the example deliberately exhibits

A reference implementation should run end-to-end and demonstrate at
least these failure modes:

1. **Webhook delivered twice.** Per-ticket lock dedupes; only one
   cascade runs.
2. **Reproduce can't construct a failing test.** Policy invalidates
   `triage`; second triage round either asks for more info (escalate)
   or finds the issue.
3. **Tests fail on first fix.** Policy invalidates `fix-attempt`;
   second attempt incorporates the failure as a constraint.
4. **Tests fail repeatedly.** Retry budget exhausts; node terminates
   `failed`; cascade routes to `human-escalation`.
5. **Agent process crashes mid-fix-attempt.** Orphan reaper recovers
   the worker-request; held claim survives the active terminal until
   auto-terminal resolves (in this case, abandons — branch deleted).
6. **PR is approved by reviewer.** `review` node terminates `fresh`;
   `merge` runs; held claim commits; ticket closes.
7. **PR is rejected by reviewer.** `review` terminal with
   `give_up` action; held claim abandons; branch deletes; ticket goes
   back to the human queue.
8. **Concurrent ticket update arrives mid-fix.** `ticket-update` peer
   fires `invalidate(['triage'])`; the active cascade is re-evaluated
   from triage forward at the next frame boundary. Held claim survives
   into the new frame if the same fix branch is still relevant.

The last one is genuinely subtle and exercises the
`worker_request.phase = 'held'` lifecycle. Worth getting right in the
reference impl.

## Reference implementation outline

Suggested layout under the rimsky repo:

```
examples/
  bug-fix-from-tickets/
    rimsky.yml
    templates/
      bug-fix.yaml          # the cascade template
    peers/
      github-lifecycle/      # GitHub webhook + lifecycle subscriber
      test-runner/           # http-node executor wrapping `go test` etc.
      claude-fix-agent/      # claude-agent executor with the fix prompt
      git-claim-producer/    # branch-lifecycle producer
    fixtures/
      sample-tickets.json
    README.md                # quickstart: docker compose up, post a webhook
```

The example should be runnable end-to-end via `docker compose up` plus
a `curl` that posts a synthetic ticket webhook. No live GitHub
account required — the fixture stubs the GitHub side.

## What this example does *not* claim to do

- It is not a replacement for human code review at the `review` node.
  The human is one of the executors. The cascade orchestrates *around*
  human review; it doesn't try to remove it.
- It is not a generic "ticket-driven CI" framework. The shape is
  opinionated: triage → reproduce → fix → test → PR → human-review →
  merge. Other shapes (ship-without-review, fix-without-tests,
  multi-step-human-iteration) are different examples or different
  templates against the same primitives.
- It does not assume Claude in particular. The agent executor is
  pluggable — `claude-agent` is the reference impl, but the executor
  protocol is model-agnostic.

## Cross-references

- Underlying primitives: `../architecture.md` (cascade, claims, frames),
  `../specs/2026-05-04-foundation-contract.md` §3-5 (lock primitives,
  atomic acquisition, auto-terminal).
- Strategic positioning: `../2026-05-02-rimsky-vs-landscape.md` §10.3
  (skill vs template framing).
- Adjacent example: `build-a-gas-town.md` (multi-agent extension of
  this same primitive set).
