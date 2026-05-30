---
concept: dry-run
status: as-is
aliases: []
---

# Dry-run

## What it is

A per-request flag — `?dry_run=true` on the control-plane request — that asks "what would happen if I did this?" without applying it. The auth middleware honors the flag, resolving the request's mode to `dry_run` regardless of the (now mode-less) grant in `concept:permission`. Default (flag absent) is `execute`. When the request resolves to `dry_run`, a write handler runs validation (including side-effect-free external calls like the validation protocol's checks; see `concept:validation`) but skips the actual mutation, returning a synthetic envelope of the form `{ dry_run: true, would_have_X: { ... } }`.

The flag is the *only* source of dry-run. There is no preview-only key to escalate from — the grant carries no mode. The auth middleware threads the resolved mode through the request context; handlers read it back from the context and gate the side-effectful path through a shared dry-run-response helper that emits the synthetic envelope.

## Purpose

Dry-run is human-in-the-loop preview-before-commit and validate-without-commit: an operator or agent can preview the effect of any write before applying it, and can validate that a request would be accepted (well-formed, authorized, structurally valid) without committing its side effect. The same audit-log trail records the attempt — "this request was previewed; we did not apply it" — as forensic evidence.

## Boundaries

Owns: the per-request `dry_run` flag handling in the auth middleware, the per-request context plumbing, the dry-run-response helper, and the per-handler dry-run branches. Dry-run covers **all** write actions uniformly — there is no auth carve-out; `auth:create` / `auth:revoke` / `auth:rotate` are previewable like any other write. Does NOT own: the read path (a read has no mutation to skip, so the flag is a no-op there; see Invariants). Adjacent: `concept:permission` (the request flag is orthogonal to the binary grant), `concept:event-log` (the audit row records `mode: dry_run`).

## Invariants

- **Reads honor the flag as a no-op.** A read has no mutation to skip, so `?dry_run=true` on a `*:read` action runs the read normally and returns it. This lets a mixed read/write script set the flag uniformly without special-casing reads. The audit row records `mode: dry_run` with `executed: true` (the read genuinely ran).
- **Every write is previewable.** Each write action has a dry-run branch returning a `would_have_*` envelope and performing no mutation. This is guaranteed structurally by a coverage conformance test that enumerates every write action and asserts each, invoked under the flag, mutates nothing — not by a runtime gate. A future write handler that omits its branch fails the test.
- **Validation runs faithfully.** Dry-run is "validate-without-mutate." For `template:register`, this includes firing the validation protocol's checks against advertising services (see `concept:validation`) — those are side-effect-free reads from the platform's perspective.
- **A request resolved to `dry_run` never mutates.** With no carve-outs, this holds by construction; the coverage conformance test is its enforcement.
- **Audit row reflects intent.** The middleware emits `auth.access_attempted` with `mode: dry_run`. For a write the row carries `executed: false`; for a read, `executed: true`. The row is the canonical evidence of "the request was previewed; we didn't apply it."

## Synthetic response shape

Each handler picks a verb that describes the intent. The synthetic envelope sets `dry_run` to `true` and carries a single `would_have_<verb>` key (`would_have_created`, `would_have_invalidated`, and so on) whose object echoes the target identifiers the live write would have produced.

For the create case, the envelope's `would_have_created` object holds a placeholder `instance_id` of `dry-run-not-persisted` (no row is created, so there is no real ID), the actual `template_hash`, and the actual `params` the create would have used.

For non-create writes the placeholder ID is replaced with the actual targets: an invalidate, for example, carries a `would_have_invalidated` object with the actual `instance_id` and `node_id` of the target being invalidated.

Clients (CLI, MCP) check the top-level `dry_run` flag to render the response distinctly from a live invocation.

## Notes

- [2026-05-15] Concept introduced by `spec:2026-05-15-control-plane-mcp-and-auth` ("Dry-run mode").
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- 2026-05-28 — instance:kill added to the dry-run-branch enumeration per spec:2026-05-28-quality-of-life-features; the force-terminate write action returns a would_have_terminated envelope under a dry_run grant.
- 2026-05-29 — Per `spec:2026-05-29-console-upstream-auth-audit-and-fixes`: dry-run becomes a per-request flag (`?dry_run=true`), not a per-grant-entry mode modifier — the grant in `concept:permission` no longer carries a mode. Coverage is uniform across all writes with no auth carve-out; `auth:create` / `auth:revoke` / `auth:rotate` are now previewable (the "auth mutations are NOT dry-runnable" carve-out is removed). Reads honor the flag as a no-op with `executed: true`. The "forced dry-run never mutates" guarantee is enforced structurally by a coverage conformance test enumerating every write action. The graduated-trust / agent-promotion narrative is dropped; purpose is human-in-the-loop preview-before-commit and validate-without-commit.
