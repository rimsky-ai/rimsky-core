---
trap: mcp-standard-methods-present
release: d977250c
demonstration: experiment:assumption-mcp-standard-methods-present
---
## Assumption

As MCP client author, I would take it that the server implements the rest of the base MCP method set — `ping`, `prompts/list`, `resources/subscribe`, `notifications/*` — beyond the five listed.

ecosystem-prior — MCP servers advertising `initialize`, `tools/list`, `tools/call`, `resources/list`, `resources/read`

## Actual behavior

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
