---
experiment: assumption-http-version-prefix-negotiable
commit: PENDING
---

# Does `/v1` name a negotiable wire version?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. It asks the
server for other version prefixes and for a version-discovery route, reads the
headers and bodies of ordinary 200s, asks the MCP skin what version the server
is, and drives the CLI at a mis-prefixed endpoint.

## What was observed

Exactly one prefix is mounted. `/v2/health`, `/v0/health`, `/health`,
`/api/v1/health` and `/v2/instances` all answer chi's plain-text
`404 page not found` — byte-identical to what a misspelled path gets, so a client
cannot tell "wrong version" from "wrong route". `/`, `/v1`, `/version`,
`/v1/version`, `/v1/versions` and `/.well-known/versions` all 404: there is no
discovery.

Nothing on the wire names a version. No response header carries one — a 200 from
`/v1/health` sends only `Connection`, `Content-Length`, `Content-Type` and
`Date` — and neither `/v1/health` nor `/v1/auth/status` returns a version field.
The one place the server names a version is the MCP `initialize` result, where
`serverInfo` reads `{"name": "rimsky-control-api", "version": "v1"}` — the path
prefix restated, not the release. The CLI meanwhile reports `rimsky v0.15.0-…`,
its own build, and never asks the server anything.

The MCP skin has a `protocolVersion` field and still does not negotiate: asked
for `2024-11-05`, `2025-06-18` and `1999-01-01`, it answered `2025-06-18` every
time.

The client does not negotiate either, it concatenates. Given
`--endpoint <base>/v2`, the CLI requests `<base>/v2/v1/instances` — `/v1` is a
literal suffix appended to whatever it is handed.

No `/v2` exists at this tree, so whether a future server would serve `/v1`
beside it is not observable here. What is observable is that the surface carries
none of the machinery that would make the question answerable at runtime.

EXPERIMENT PASS (20 checks)
