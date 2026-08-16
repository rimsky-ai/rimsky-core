---
assumption: mcp-catalog-hides-denied-tools
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `tools/list` returns only tools the calling key may actually invoke, so a listed tool never fails with a permission error.

As agent holding a narrow key, I would take it that `tools/list` returns only tools the calling key may actually invoke, so a listed tool never fails with a permission error.

## Source

published-concept — `concept:control-api` ("catalog is computed from the canonical action registry and filtered by the requesting key's permission grant")

## What a run would observe

list tools with a `read-only` key, then call every listed tool and check none returns a permission denial.

## Measured

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
