---
decision: mcp-base-methods-scope
---

# The control API's MCP server implements six base methods

## Choice

The control API's MCP server answers `initialize`, `ping`, `tools/list`, `tools/call`, `resources/list`, and `resources/read`. It does not implement `prompts/*`, `resources/subscribe`, `resources/templates/list`, `roots/*`, `sampling/*`, or `logging/setLevel`. The capabilities the server returns from `initialize` name exactly what it serves, and a request for an unimplemented method receives the protocol's method-not-found error.

## Rationale

The MCP surface exists to project the HTTP control surface onto agents (see `decision:mcp-http-parity`). Tools and read-only resources are that projection. `ping` costs nothing and every client library sends it. Prompts, subscriptions, roots, sampling, and log-level control are client-side or agent-side features that project no part of the control surface; implementing them would add a second semantics with nothing behind it. Declaring the served set in the capabilities and answering the rest with method-not-found lets a client discover the boundary instead of guessing at it.

## Alternatives

- The whole base method set, with empty results for the parts rimsky has nothing behind — rejected: an empty `prompts/list` is a lie about what the server offers, and a no-op `logging/setLevel` is a control that controls nothing.
- Tools only, no `ping` — rejected: clients send `ping` as a liveness check, and a method-not-found on it reads as a broken server.
