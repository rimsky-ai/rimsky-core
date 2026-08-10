---
experiment: attribute-carry-forward
commit: PENDING
---

# An executor's own output attribute on the next dispatch, and a fresh bag in a new run-scope

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` with the
bundled filesystem claim producer configured over a throwaway workspace, and
drives it through the control API. The stateful node is the bundled
`loop_counter` kind: its executor writes a `count` attribute and reads the
incoming `count` to decide the next one, so the sequence of counts a run emits
is a direct readout of what the incoming attribute bag held. One template lets
that node cascade to itself, so it dispatches repeatedly inside one run-scope.
A second template puts the same node behind a fan-out over three partitions, so
the same node runs once in each of three partition run-scopes.

## What was observed

Inside one frame the self-cascading node dispatched three times and emitted
counts 1, 2, 3 — each dispatch saw the value the previous dispatch's executor
wrote. The node read surface then answered with the resolved bag
`{count: 3, max: 3}`.

A second operator message opened a second frame, and the same node emitted
1, 2, 3 again rather than continuing at 4: the new frame's run-scope began at
the schema's defaults.

The fan-out opened three partitions keyed `p1`, `p2`, `p3`, and every partition's
dispatch emitted count 1. No partition continued a sibling's count, so each
partition run-scope also began at the schema's defaults.

The story names three kinds of new run-scope; this experiment measures two of
them (a fan-out partition and a new frame). The third, a sub-graph invocation,
is not measured here: every probe that put a bundled node kind inside a
delegated sub-graph left the sub-graph's child run enqueued and never
dispatched, so no such probe reached a state that could be read either way.

Five checks, none failing.
