---
issue: unreachable-service-row-stall
kind: human
category: unspecified
artifacts:
  - concept:executor
  - concept:claim-producer
  - concept:supervisor
  - concept:error-policy
status: promoted
sprint: 2026-07-25-issue-drain-2026-07-22-batch.md
opened: 2026-07-22T02:37:13Z
---

# Work aimed at a service nobody runs waits in the queue forever, silently

A queued unit of work in rimsky names the executor (the service that does the actual work — an LLM call, an HTTP call) that should run it. If no running supervisor — the process that claims queued work and dispatches it — is configured to accept that executor name (a typo, a decommissioned service, a template nobody finished deploying), the row is never claimed, never errors, and never alerts. It just waits, forever, invisibly. A second, adjacent situation looks similar but isn't: work routed through the proxy to a developer's own machine stalls whenever that developer's agent is offline — a *normal* transient state, not a failure, but one an operator may still want to alert on past some threshold.

What re-verification found: the two cases have opposite amounts of coverage. The never-claimed case is a true gap — supervisors only check "does this executor actually exist?" *after* claiming a row, and a row nobody's accept-list matches never gets claimed, so it never reaches any check at all. The offline-agent case is largely solved already: an unreachable agent surfaces as an ordinary executor error, and the standard retry policy (retry up to a declared cap, then settle the run as failed) already provides threshold-then-escalate — the only open question is whether "eventually fail the run" is the right shape for a state that's supposed to be normal. The design corpus describes these mechanics accurately but never says whether either state is acceptable.

## Options

- **Offline-agent case:** document the existing retry-cap-give-up chain as the sanctioned escalation (documentation only), or build a new alert signal that doesn't require modeling a disconnected agent as a failed run.
- **Never-claimed case:** a periodic sweep that flags any row unclaimed past a window when no registered supervisor accepts its executor name — firing a synthetic error class the way "claimed but executor unknown" already does; or upfront validation at template registration (weaker — accept-lists change after registration); or document "keep your fleets and templates in sync" as an operator responsibility and accept the silence.
- **Rule the two cases independently** — one fix conflating them would re-solve the covered case while under-specifying the real gap.

The ruling decides: is documentation enough for the offline-agent case; does the never-claimed case get a sweep, a registration gate, or an accepted silence; and where any threshold lives.

## Ruling

> Owner ruling (2026-07-25, live). Case 1 (agent-not-connected behind
> the proxy): the existing retry + MaxRetries + give-up chain is the
> sanctioned escalation — document it in concept:error-policy, no new
> mechanism. Case 2 (never-claimed rows): no sweep — eliminate the
> condition structurally with a shared executor address book. The
> per-supervisor accepted_executors accept-list is a boot-time
> snapshot of rimsky.yml, and every stall scenario is that snapshot
> going stale; so: the control API publishes cfg:executors into a
> shared address-book table on boot and config reload; supervisors
> drop accept-list registration and claim-time name filtering (their
> DB registration keeps per-process facts: concurrency, callback);
> the resolver becomes read-through against the table with a short
> TTL cache (same pattern as the late-bind host-agent resolver);
> template registration validates executor names against the same
> table, making the check authoritative. A name that resolves nowhere
> then fails inside a claimed dispatch and lands in the existing
> loud unresolved-executor error path — the silent stall becomes
> unrepresentable. Universal reachability is declared a deployment
> requirement: all supervisors reach all declared executors; there is
> no implicit reachability partitioning, and if partitioning is ever
> genuinely needed it must arrive as a deliberate, explicit feature,
> not as a side effect of per-process config.
>
> Rationale (owner, live): supervisors were always meant to be
> universal; the accept-list was config plumbing, not policy. A
> supervisor in one process should never hang on to another runtime
> moment's executor list — it should be handed the current one or
> refresh it from shared state when it goes to work.
>
> Extension (owner, live, at /plan-sprint 2026-07-25): the address
> book covers claim-producer stores identically. The accepted-store
> set is the same boot-time snapshot with the same silent-stall mode,
> so supervisors drop it too — no accept-list of any kind survives,
> no service name participates in claim-time candidate filtering, and
> the universal-reachability deployment requirement covers stores as
> well as executors.
