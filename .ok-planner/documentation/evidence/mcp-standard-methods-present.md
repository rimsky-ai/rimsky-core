---
trap: mcp-standard-methods-present
release: d977250c
---
# Evidence set — the server implements the rest of the base MCP method set — `ping`, `prompts/list`, `resources/subscribe`, `notifications/*` — beyond the five listed.

Source of the prior: ecosystem-prior — MCP servers advertising `initialize`, `tools/list`, `tools/call`, `resources/list`, `resources/read`

## What the audit ran and observed (assumption record)

Ran `experiments/assumption-mcp-standard-methods-present` (23 checks, pass)
against one `rimsky-all-in-one` container at this tree, issuing twelve
base-protocol methods beyond the documented five.

The five documented methods are the whole implementation, and the prior is
contradicted on the ten of those twelve that are genuine requests: `ping`,
`prompts/list`, `prompts/get`, `resources/subscribe`, `resources/unsubscribe`,
`resources/templates/list`, `completion/complete`, `logging/setLevel`,
`roots/list` and `sampling/createMessage` each answer JSON-RPC `-32601 method not
found`, indistinguishable from an invented method name. `ping` is the one that
costs a client author most: it is base protocol rather than capability-gated, so
nothing in the handshake warns that a keepalive will be refused.

The two `notifications/*` entries are a different case, and the probe overstated
them. It sent every method as a request carrying an `id`, which is what produced
their `-32601`. A conforming client sends a lifecycle notification with no `id`,
and the server tests for that before it dispatches on the method name: any
notification is accepted and ignored whatever it is called. So
`notifications/initialized` — the notification a conforming client sends
unprompted right after `initialize` — is handled correctly, and the earlier
reading that a strict client could fail its handshake on it does not hold.

The mitigating half is real and worth recording: `initialize` advertises exactly
two capabilities, `tools` and `resources`, with
`resources: {"subscribe": false, "listChanged": false}` and no `prompts` or
`logging` key. A client that reads capabilities is correctly told that subscribe
and prompts are absent, which leaves `ping` as the one absence no declaration
warns about.

## Experiment record (experiment:assumption-mcp-standard-methods-present)

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

Runnables: `src:.ok-planner/experiments/assumption-mcp-standard-methods-present/` at the stamped commit.
