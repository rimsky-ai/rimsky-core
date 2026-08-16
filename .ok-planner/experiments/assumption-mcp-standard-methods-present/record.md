---
experiment: assumption-mcp-standard-methods-present
commit: PENDING
---

# Which MCP methods does the server implement?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. It opens an MCP
session over `POST /v1/mcp`, exercises the five documented methods, then issues
twelve more base-protocol methods and one invented name, and reads the
capabilities the server advertises at `initialize`.

## What was observed

The five documented methods dispatch. Every other base method answers JSON-RPC
`-32601 method not found`, naming the method back: `ping`,
`notifications/initialized`, `notifications/cancelled`, `prompts/list`,
`prompts/get`, `resources/subscribe`, `resources/unsubscribe`,
`resources/templates/list`, `completion/complete`, `logging/setLevel`,
`roots/list` and `sampling/createMessage`.

`notifications/initialized` is the one a conforming client sends unprompted right
after `initialize`. The server rejects it rather than ignoring it — though the
rejection is not fatal: the session still answers `tools/list` afterwards.

The server does declare what it has. `initialize` advertises exactly two
capabilities, `tools` and `resources`, with
`resources: {"subscribe": false, "listChanged": false}` — matching the missing
subscribe method — and no `prompts` or `logging` capability at all. So a client
that reads capabilities is warned about the capability-gated absences. `ping` and
`notifications/initialized` are base protocol rather than capability-gated, and
are absent all the same.

A missing base method and a method that never existed are indistinguishable: the
invented `totally/made/up` answers the same `-32601` shape.

EXPERIMENT PASS (23 checks)
