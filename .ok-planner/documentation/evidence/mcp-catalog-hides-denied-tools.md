---
trap: mcp-catalog-hides-denied-tools
release: d977250c
---
# Evidence set — `tools/list` returns only tools the calling key may actually invoke, so a listed tool never fails with a permission error.

Source of the prior: published-concept — `concept:control-api` ("catalog is computed from the canonical action registry and filtered by the requesting key's permission grant")

## What the audit ran and observed (assumption record)

Ran `experiments/assumption-mcp-catalog-hides-denied-tools` (10 checks, pass)
against one `rimsky-all-in-one` container at this tree, listing and then calling
every tool with an admin key, a `*:read` key, and a key whose grant scopes
`tag:delete` to one tag.

For a plain action-level grant the prior holds exactly as `concept:control-api`
says: admin sees 57 tools, a `*:read` key sees 30, no mutating tool is listed for
the reader, and calling all 30 listed tools with that key produced zero
permission denials.

Scope breaks it. A key granted `*:read` plus `tag:delete` scoped to
`template_tag: mine:v1` is shown 31 tools with `tag_delete` among them. Calling
that listed tool on `theirs:v1` returns `{"error": "permission denied"}`,
`status: 403`, `isError: true`; calling the same listed tool on `mine:v1`
succeeds. The catalog is computed per key and scope is evaluated per request
target, so no per-key listing can express "this tool, but only for these
arguments" — the agent's only way to learn the boundary is to hit it.

This is the grant's shape, not the MCP skin's: the same key gets 403 on
`DELETE /v1/tags/theirs:v1` over HTTP. It is narrow — scope is accepted on only
seven actions at this tree (see `http`-side assumption
`permission-scope-on-every-action`) — but on those seven a listed tool can and
does fail with a permission error.

## Experiment record (experiment:assumption-mcp-catalog-hides-denied-tools)

# Does `tools/list` only show what the key may invoke?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, seeded with a
template, an instance and two tags. It lists tools with an admin key, with a
`*:read` key, and with a key whose grant scopes `tag:delete` to one tag, then
calls every listed tool with each key and looks for permission denials.

## What was observed

The action-level filtering works exactly as claimed. The admin key sees 57 tools
and the `*:read` key sees 30; no tool whose name carries a mutating verb is
listed for the read-only key. Calling all 30 listed tools with that key produced
zero permission denials.

Scope is the case the catalog cannot see. A key granted `*:read` plus
`tag:delete` scoped to `template_tag: mine:v1` is shown 31 tools, `tag_delete`
among them. Calling that listed tool on `theirs:v1` returns
`{"error": "permission denied"}` with `status: 403` and `isError: true`; calling
the same listed tool on `mine:v1` succeeds. One listed tool, two verdicts,
decided by an argument the catalog never sees — `tools/list` is computed per key,
while scope is evaluated per request target.

The same split shows over HTTP: `DELETE /v1/tags/theirs:v1` with that key answers
403 `{"error": "permission denied"}`. So this is the grant's shape, not an
artifact of the MCP skin.

EXPERIMENT PASS (10 checks)

Runnables: `src:.ok-planner/experiments/assumption-mcp-catalog-hides-denied-tools/` at the stamped commit.
