---
experiment: attribute-carry-forward
commit: d977250c
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

Five checks, none failing.

## way-subgraph-scope.py

### What it runs against

The story names three kinds of new run-scope, and the third — a sub-graph
invocation — needs its own way. This one drives two templates that differ in a
single key. The first is a sub-graph whose internal node names an executor. The
second is the same template with that node changed to the bundled stateful kind,
so the count it emits would read out what its incoming bag held.

### What was observed

The first template settles: the caller dispatches, the internal node runs, the
exit runs, and the frame completes. The second does not. The internal node's run
is created and sits queued; it is never dispatched, no count is ever emitted, and
the frame stays running indefinitely. The story's third run-scope kind therefore
has no measurement — not because the reset was observed to fail, but because no
run reaches a state that would show the incoming bag either way.

Four checks, three failing.
