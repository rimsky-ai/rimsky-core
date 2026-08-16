---
experiment: assumption-mcp-resource-uris-are-a-family
commit: PENDING
---

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
