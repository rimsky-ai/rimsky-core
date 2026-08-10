---
audit: claude-agent
artifact: story:claude-agent
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# An operator wires an agentic node with per-node declarations, a sign-off gate and error classes

Supported. All four capabilities the story names were driven in one template
against an all-in-one deployment carrying the bundled claude-agent executor.
Every agent node's work was handed off asynchronously and settled later by
callback. A node declaring an inline MCP server and an inline expose-env name
got exactly those and nothing another node declared, while the two operator
allowlists were set to one entry each. The sign-off gate committed the run whose
signature covered the value the run wrote out, and refused with
`agent/signoff_unobtained` both a signature bound to another dispatch and a
signature taken over a value the run never wrote. The executor advertises
thirteen declared error classes over the control API; of those, one routed by
node policy settled the run fresh while its settling signal still named the
class, one with no policy failed the run under its own name, a wildcard
subscription over the family ran on that failure, and a class outside the
vocabulary was refused at the callback surface.

## Compliance

The benefit clause promises "controllable, secure, observable agentic
dispatches" — three adjectives describing how well the product owes something,
which a story may not rest on. Compliant text names what the operator can then
do, for example "so that I bound what each node's agent may reach, refuse any
run whose output is not signed by a key I named, and route each declared failure
class myself".
