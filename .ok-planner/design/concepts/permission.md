---
concept: permission
status: as-is
aliases:
  - grant
  - action
---

# Permission

## What it is

The per-key authorization grant attached to a `concept:api-key`. Each key carries a JSON array of grant entries; each entry pairs an action string with an optional mode (an identity-bound dry-run floor owned by `concept:dry-run`, defaulting to full execution) and an optional scope (a resource selector evaluated alongside the action match).

The grant comprises four pieces: the grant-entry types and their parser, the wildcard matcher and validator, the set-membership permission evaluator, and the canonical action registry.

## Purpose

The auth middleware needs a small, predictable grammar for "what this key is allowed to do." Forward-compatibility matters — entries grow new fields (`mode` and `scope`) without a schema migration — so entries are JSON with a parser that preserves unknown fields.

## Boundaries

Owns: the grant entry shape, a boundary-only wildcard matcher over the action grammar, the canonical action registry (which includes the `service:enroll` verb that authorizes a service to enroll for a certificate identity), and per-action resource scoping via the optional selector field. Does NOT own: per-route handler dispatch (that's the HTTP router's concern), role expansion (CLI-side; see `concept:role-template`), the resolution of preview-vs-commit (`concept:dry-run` owns resolving the request's mode; `concept:permission` owns only the grant mode field that feeds the floor), the certificate machinery gated by the `service:enroll` grant (that's `concept:peer-auth`). Adjacent: `concept:api-key`, `concept:control-api`, `concept:dry-run` (owns resolving the request's mode from the flag and this concept's grant mode field together), `concept:role-template`, `concept:peer-auth`.

## Invariants

- **Closed action grammar.** An action is a noun:verb pair joined by a single separator, and the wildcard vocabulary is exactly three forms: the full wildcard, a noun-scoped wildcard (every verb of one noun), and a verb-scoped wildcard (one verb across every noun). The separator is part of the match boundary — a noun-scoped wildcard never matches a longer noun — and no other wildcard shape (infix, pattern) exists; an invalid form is rejected at key creation.
- **Set-membership evaluation.** A request is allowed iff some entry's action matches AND that entry's scope (if present) is satisfied by the request's target resource; otherwise denied. Iteration order is irrelevant — any matching, in-scope entry allows, so there is no first-match-wins rule.
- **Scoped entries are least-privilege.** A scope-bearing entry allows ONLY requests whose target resource satisfies the selector; an out-of-scope request of the same action is denied unless another entry independently allows it.
- **Grant mode is a floor.** The matched entry's mode (full execution by default) is the most permissive mode the request may run at; the dry-run flag may restrict further but never escalate (see `concept:dry-run`).
- **Forward-compatible parser.** Unknown JSON fields on grant entries are preserved (round-tripped through marshal).
- **Action registry is canonical.** The same registry validates key-creation request bodies (unknown action strings → 400) and resolves MCP tool names → action → handler.
