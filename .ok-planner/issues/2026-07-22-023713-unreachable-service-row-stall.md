---
issue: unreachable-service-row-stall
kind: human
category: unspecified
artifacts:
  - concept:executor
  - concept:claim-producer
  - concept:supervisor
  - concept:error-policy
status: verified
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

> Recommended ruling (/recommend-rulings): Case 1 (agent-not-connected
> behind the proxy): the existing retry + MaxRetries + give-up chain
> is the sanctioned escalation — document it in concept:error-policy,
> no new mechanism. Case 2 (no supervisor's accepted_executors covers
> the row): add a supervisor-side sweep emitting a synthetic error
> class analogous to unresolved_executor when a row has sat unclaimed
> with no registered claimant; no template knob — the sweep is
> deployment-level.
>
> Rationale: Case 1 is solved machinery wearing a documentation gap.
> Case 2's sit-in-queue-forever-invisibly violates the fail-loudly
> posture the platform applies everywhere else, and a template
> threshold is the wrong home for a condition that has nothing to do
> with any dispatch attempt.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
