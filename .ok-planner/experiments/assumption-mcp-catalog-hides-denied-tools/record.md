---
experiment: assumption-mcp-catalog-hides-denied-tools
commit: PENDING
---

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
