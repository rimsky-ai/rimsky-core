---
trap: stub-mode-on-every-bundled-executor
release: d977250c
demonstration: experiment:assumption-stub-mode-on-every-bundled-executor
---
## Assumption

As template author testing a graph offline, I would take it that every bundled executor honors a `stub_response` attribute, so a whole template can be exercised without live upstreams.

sibling-symmetry — `stub_response` on `claude-agent` and `http-node` but not on `verifier-http` or `verifier-shape-checks`

## Actual behavior

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
