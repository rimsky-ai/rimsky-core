---
issue: no-per-run-lifecycle-state-endpoint
kind: audit
category: decision-drift
artifacts:
  - decision:node-state-retired-from-operator-api
status: promoted
opened: 2026-08-02T09:58:20Z
sprint: 2026-08-03-audit-gap-drain.md
---

# The per-run state endpoint the operator API promises was never built

When rimsky retired the synthesized per-node `state` field from the operator API, the decision named two replacement surfaces: a categorical per-state run summary on the node read (built and real), and "the per-run endpoint for a specific run's state" — the surface answering the question operators actually ask when they have one run in mind (`decision:node-state-retired-from-operator-api`). Enumerating all 14 route groups registered on the control API finds no such endpoint. The only run-keyed read is the lineage projection (`route:GET /v1/lineage/runs/{run_id}`), which is a different animal: it is populated only when a leaf run reaches terminal, and it reports a provenance outcome, not a lifecycle state — an in-flight or non-terminal run returns nothing.

So the retirement's replacement story is half-delivered: an operator watching a specific stuck run has the summary's aggregate counts and no way to ask "what state is run X in." The ruling decides whether the missing surface gets built or the decision retreats to naming only the summary.

## Options

- Build the per-run endpoint (a run-id-keyed GET returning the node-run lifecycle state), with its MCP tool in the same change so the transport-parity claim doesn't immediately reopen. Cost: real new-route work — auth action, response shape, tool registration.
- Amend the decision to name only the categorical summary as the replacement surface. Cost: the operator question the decision itself says the endpoint exists to answer goes officially unanswerable.

## Ruling

> Recommended ruling (/verify-issues): build the per-run state endpoint, and register its MCP tool in the same change.
>
> Rationale: the decision's own rationale names the question this surface answers, and the alternative amends the corpus into admitting operators can't ask it — a retreat with no offsetting gain, since the route is ordinary read-path work. The lineage read cannot absorb the job (terminal-only, different vocabulary). Rule this alongside the MCP-parity gap issue; both touch the same surface-completeness theme. Flip case: if operator workflows in practice always start from the summary and drill down via the event log (i.e. no one ever asks the per-run question), amend the decision instead and retire the clause honestly.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
