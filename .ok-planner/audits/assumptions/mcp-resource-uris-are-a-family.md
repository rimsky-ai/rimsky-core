---
assumption: mcp-resource-uris-are-a-family
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# the `rimsky://` URI scheme covers more than breakpoint hits — instances, nodes, and events are addressable as resources too.

As agent reading debugger state, I would take it that the `rimsky://` URI scheme covers more than breakpoint hits — instances, nodes, and events are addressable as resources too.

## Source

sibling-symmetry — two `rimsky://` resources exist for breakpoint hits while the REST surface exposes dozens of readable resources

## What a run would observe

`resources/list` and check whether any `rimsky://` URI outside the two breakpoint-hit forms is offered or resolvable.

## Measured

Ran `experiments/assumption-mcp-resource-uris-are-a-family` (23 checks, pass)
against one `rimsky-all-in-one` container at this tree, calling `resources/list`
and asking `resources/read` for fourteen `rimsky://` URIs covering the readable
REST nouns.

The `rimsky://` scheme is exactly two forms, both about breakpoint hits, and
nothing else in it resolves. `rimsky://instances`, `rimsky://instances/{id}`,
per-instance nodes, frames, events and messages, `rimsky://nodes/{id}`,
`rimsky://events`, `rimsky://templates`, `rimsky://templates/{id}`,
`rimsky://tags`, `rimsky://runs/{id}`, `rimsky://audit` and
`rimsky://observability/executors` are each rejected `-32602`, and the rejection
enumerates the whole scheme back: "uri must be
rimsky://instances/{uuid}/breakpoint-hits or rimsky://breakpoints/{uuid}/hits".
`resources/templates/list`, the method that would advertise a URI family, is not
implemented.

Two further details sharpen it. `resources/list` is derived from instances, not
breakpoints — with one instance and no breakpoints it already offers the
instance-scoped URI, and creating a breakpoint adds nothing. And the second
documented form, `rimsky://breakpoints/{bid}/hits`, resolves and reads but is
never listed; it appears only inside the listed resource's description text, so
an agent that trusts `resources/list` will not find it.

Meanwhile eight readable REST resources — the instance, its nodes, frames and
messages, events, templates, tags, the audit log — all answer 200 over HTTP with
no `rimsky://` address. An agent that reaches for resources rather than tools
finds one debugger feed and must fall back to `tools/call` for everything else.
