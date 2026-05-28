---
concept: dry-run
status: as-is
aliases: []
---

# Dry-run

## What it is

A per-grant-entry modifier on `concept:permission` entries: `{ "action": "<action>", "mode": "dry_run" }`. When the per-request mode resolves to `dry_run`, the handler runs validation (including side-effect-free external calls like the validation protocol's checks; see `concept:validation`) but skips the actual mutation, returning a synthetic envelope of the form `{ dry_run: true, would_have_X: { ... } }`.

The control-plane auth middleware threads the resolved mode through the request context; handlers read it back from the context and gate the side-effectful path through a shared dry-run-response helper that emits the synthetic envelope.

## Purpose

Agentic supervision needs a way to ask "what would happen if I did X?" without applying X. The same audit-log trail then serves as evidence of agent intent — "this agent attempted these operations; we did not apply them" — which is the precondition for promoting an agent from supervised dry-run to live execution.

## Boundaries

Owns: the per-grant-entry mode vocabulary (`execute` / `dry_run`), the per-request context plumbing, the dry-run-response helper, the per-handler dry-run branches (instance:create, instance:terminate, template:register, template:deploy/undeploy/deregister, tag:create/set/delete, node:invalidate/reset, message:send, lineage:prune, backfill:create/cancel, asset:materialize/delete). Does NOT own: auth mutations (those ignore mode by design; see Invariants below). Adjacent: `concept:permission`, `concept:event-log` (the audit row reflects `executed: false`).

## Invariants

- **Read actions ignore mode.** The dry-run modifier is meaningful only for write actions.
- **Validation runs faithfully.** Dry-run is "validate-without-mutate." For `template:register`, this includes firing the validation protocol's checks against advertising services (see `concept:validation`) — those are side-effect-free reads from the platform's perspective.
- **Audit row reflects intent.** The middleware emits `auth.access_attempted` with `mode: dry_run` and `executed: false`. The row is the canonical evidence of "the agent attempted this; we didn't apply it."
- **Auth mutations are NOT dry-runnable.** `auth:create`, `auth:revoke`, `auth:rotate` always execute regardless of the matching grant entry's mode. Rationale: dry-running auth mutations doesn't compose well with the implicit-anonymous predicate (a dry-run create wouldn't change the active-key count, leading to confusing audit trails).

## Synthetic response shape

Each handler picks a verb that describes the intent:

```json
{ "dry_run": true, "would_have_created": { "instance_id": "dry-run-not-persisted", "template_hash": "<actual>", "params": { ... } } }
```

For non-create writes the placeholder ID is replaced with the actual target:

```json
{ "dry_run": true, "would_have_invalidated": { "instance_id": "<actual>", "node_id": "<actual>" } }
```

Clients (CLI, MCP) check the top-level `dry_run` flag to render the response distinctly from a live invocation.

## Notes

- [2026-05-15] Concept introduced by `spec:2026-05-15-control-plane-mcp-and-auth` ("Dry-run mode").
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
