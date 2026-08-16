---
assumption: http-version-prefix-negotiable
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# the `/v1` prefix means the server can serve more than one wire version at once, so a client pinned to `/v1` keeps working after the server adds `/v2`.

As integrator planning an upgrade, I would take it that the `/v1` prefix means the server can serve more than one wire version at once, so a client pinned to `/v1` keeps working after the server adds `/v2`.

## Source

ecosystem-prior — a versioned URL prefix on an HTTP API

## What a run would observe

ask the server for an unknown version prefix and check whether anything about the surface suggests concurrent versions, and whether the CLI negotiates.

## Measured

Ran `experiments/assumption-http-version-prefix-negotiable` (20 checks, pass)
against one `rimsky-all-in-one` container at this tree.

The `/v1` prefix is a path literal, not a version contract, and the surface
carries none of the machinery an integrator would use to check an upgrade.
`/v2/health`, `/v0/health`, `/health` and `/api/v1/health` all answer chi's
plain-text `404 page not found` — byte-identical to a misspelled path — so a
client cannot distinguish "this server does not speak that version" from "you
typed the route wrong". There is no discovery route: `/`, `/v1`, `/version`,
`/v1/version`, `/v1/versions` and `/.well-known/versions` all 404. No response
header names a version; neither `/v1/health` nor `/v1/auth/status` returns one.
The CLI does not negotiate, it concatenates: `--endpoint <base>/v2` produces a
request to `<base>/v2/v1/instances`.

The one place the server names a version, it names the prefix back: MCP
`initialize` reports `serverInfo: {"name": "rimsky-control-api", "version":
"v1"}`, a constant, while the CLI reports its own build `v0.15.0-…` and never
asks the server. An integrator planning an upgrade cannot ask a running server
what release it is or what wire shape it serves. The MCP skin even has a
`protocolVersion` field and ignores it — asked for `2024-11-05`, `2025-06-18`
and `1999-01-01`, it answered `2025-06-18` every time.

What no run at one tree can observe: whether a future server would serve `/v1`
beside a new `/v2`. The trap is not a failed upgrade — it is that the prefix
advertises a version contract the product implements nothing behind, so a client
"pinned to /v1" has pinned a string it can never verify against the server.
