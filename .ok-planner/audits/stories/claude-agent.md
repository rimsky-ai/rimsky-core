---
audit: claude-agent
artifact: story:claude-agent
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:01:18Z
---

# The bundled agent executor dispatches async work under operator bounds, a sign-off gate and declared error classes

Supported. Driven through the public surface against a released-image stack
running the bundled agent executor with a stand-in agent binary speaking the same
CLI and callback contract, on one template of seven nodes exercising each clause,
with both operator allowlists set to one entry each. Twelve checks, none failing.
The executor advertised thirteen declared error classes over the control API, and
every agent node's work was handed off asynchronously and settled later by
callback. The worker's agent received exactly its node's own inline MCP server
plus the executor's callback server, and exactly its node's own declared
environment variable at the operator-set value, so the per-node declaration and
the operator bound met. The sign-off gate held in all three directions: the run
whose signature covers the value it writes committed that same value, while a
signature bound to another dispatch and a signature over a value the run never
wrote both failed as sign-off unobtained. The error-class handling answered on
every leg: a declared class routed by policy settled the run fresh while the
signal still named the class, a declared class with no policy failed the run
under its own name, a wildcard subscriber over the agent error family ran on that
failure, and a class outside the declared vocabulary was refused at the callback
surface with the dispatch failing under a declared class instead. Each adjective
in the story's benefit rests on one of these measured clauses rather than on
judgment.
