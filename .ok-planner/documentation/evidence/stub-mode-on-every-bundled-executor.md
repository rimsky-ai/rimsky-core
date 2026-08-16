---
trap: stub-mode-on-every-bundled-executor
release: d977250c
---
# Evidence set — every bundled executor honors a `stub_response` attribute, so a whole template can be exercised without live upstreams.

Source of the prior: sibling-symmetry — `stub_response` on `claude-agent` and `http-node` but not on `verifier-http` or `verifier-shape-checks`

## What the audit ran and observed (assumption record)

The experiment `assumption-stub-mode-on-every-bundled-executor` set
`stub_response` on one node per bundled executor, with all four started under
`RIMSKY_EXECUTOR_STUB_MODE=1` — the most favourable condition the prior could
ask for. Every schema accepted the attribute at registration and three
different things happened at dispatch. `http-node` honoured it and settled with
the author's own object as the delta. `claude-agent` honoured it only when the
node also declared `stub_probe: true`; without that the author's key was
dropped and the executor's canned `{"stub": true}` came back. `verifier-http`
ignored it outright, settling with the canned marker and the change summary
`verifier-http stub`. `verifier-shape-checks` did not stub at all — the node
failed with `verifier/attribute_invalid` asking for the real row data its check
needs. The attribute is also not the template author's to use: the same node
against an ordinary http-node failed with `http/network_error`, having made the
real request, because honouring `stub_response` depends on an operator env flag
on the executor process. A template author cannot exercise a graph offline by
setting `stub_response`; on half the bundled executors it does nothing, and on
none of them does it work without the operator's cooperation.

## Experiment record (experiment:assumption-stub-mode-on-every-bundled-executor)

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

Runnables: `src:.ok-planner/experiments/assumption-stub-mode-on-every-bundled-executor/` at the stamped commit.
