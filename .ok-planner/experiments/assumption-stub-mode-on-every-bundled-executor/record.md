---
experiment: assumption-stub-mode-on-every-bundled-executor
commit: d977250c
---

# Setting stub_response on a node per bundled executor

## What it ran against

One `rimsky-all-in-one` stack with all four bundled executors wired in, each
started with `RIMSKY_EXECUTOR_STUB_MODE=1`, plus a fifth container running
`rimsky-executor-http-node` in its ordinary mode. Each executor gets one node
whose attributes declare `stub_response`, and the run reads the settled state
and the attributes delta from the control API.

## What was observed

Every executor's advertised schema accepted the attribute at registration, and
three different things happened at dispatch.

`http-node` honoured it: the node settled `fresh` with the author's own object
as the delta, `{"canned": true}`.

`claude-agent` honoured it only under a second attribute. With
`stub_probe: true` the delta carried the author's `answer` key; without
`stub_probe` the author's key was dropped and the executor's canned marker
`{"stub": true}` came back instead.

`verifier-http` ignored it: the node settled `fresh` with `{"stub": true}` and
the change summary `verifier-http stub`, so the author's canned response never
reached the graph.

`verifier-shape-checks` did not stub at all: the node `failed` with
`verifier/attribute_invalid` — "attributes.rows (array) required: check
`row_count` needs row-level data" — the real check path asking for real data.

The attribute is also gated on the operator, not the template. The same
`stub_response` node pointed at the ordinary http-node `failed` with
`http/network_error` on `Get "http://nowhere.invalid/x"`: the executor made the
real request the author meant to stub out.
