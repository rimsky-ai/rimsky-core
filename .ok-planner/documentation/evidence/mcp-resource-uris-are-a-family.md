---
trap: mcp-resource-uris-are-a-family
release: d977250c
---
# Evidence set — the `rimsky://` URI scheme covers more than breakpoint hits — instances, nodes, and events are addressable as resources too.

Source of the prior: sibling-symmetry — two `rimsky://` resources exist for breakpoint hits while the REST surface exposes dozens of readable resources

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-mcp-resource-uris-are-a-family)

# How much of rimsky does the `rimsky://` scheme address?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag with one instance
and one breakpoint. It calls `resources/list`, reads both documented URI forms,
then asks `resources/read` for fourteen other `rimsky://` URIs covering the
readable REST nouns, and checks how many of those nouns the REST surface serves.

## What was observed

The scheme is two forms and no more. `resources/list` is derived from instances:
with one instance and no breakpoints it already offers one URI,
`rimsky://instances/{iid}/breakpoint-hits`, and adding a breakpoint adds nothing.
The second documented form, `rimsky://breakpoints/{bid}/hits`, resolves and reads
but is never listed — it is only mentioned in the listed resource's description
text.

Both breakpoint-hit forms read, returning
`application/x-rimsky-breakpoint-hits+json`.

Every other `rimsky://` URI is rejected with `-32602`. Fourteen were tried —
`rimsky://instances`, the single-instance form, per-instance nodes, frames, events
and messages, `rimsky://nodes/{id}`, `rimsky://events`, `rimsky://templates` and
the single-template form, `rimsky://tags`, `rimsky://runs/{id}`,
`rimsky://audit`, `rimsky://observability/executors`. The rejections come in two
messages, `unknown uri shape: …` and
`uri must be rimsky://instances/{uuid}/breakpoint-hits or
rimsky://breakpoints/{uuid}/hits (got …)`; neither offers any other form.
`resources/templates/list`, the method that would advertise a URI family, answers
`method not found`.

Meanwhile eight readable REST resources — the instance, its nodes, frames and
messages, events, templates, tags and the audit log — all answer 200 and none has
a `rimsky://` address.

EXPERIMENT PASS (23 checks)

Runnables: `src:.ok-planner/experiments/assumption-mcp-resource-uris-are-a-family/` at the stamped commit.
