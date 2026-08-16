---
experiment: node-admin
commit: PENDING
---

# Reading a node's whole state, and clearing a settled failure off it

## What it ran against

A `rimsky-all-in-one` stack and a `rimsky-executor-verifier-shape-checks`
service, both from the tree's own image tag, on one docker network. One
template declares two nodes against that executor with the same declared
check: one is given clean rows and one is given a row that violates the check,
so a single instance ends with one node settled successful and one settled
failed. The reads and the clearing go through the operator CLI's `instance
nodes`, `node get` and `admin reset` verbs and through the control API's node
read, node reset and event-log routes.

## What was observed

Twenty-seven checks, none failing. Both nodes settled: the clean one fresh,
the violating one failed with the check's own error class. The control API's
node read returned the node's whole state in one document of ten fields — id,
instance, node type, executor, declared tags, cascade mode, creation time, the
run tallies, the attributes the run left behind (including the offending row
and the check that rejected it), and the settled signal
`terminal/error/verifier/check_failed/no_nulls`. The healthy node's same read
carried `terminal/success`, so the marker distinguishes the two. The CLI's
`node get` renders a narrower seven-field projection of that document —
identity, executor, run tallies and the settled signal, without the tags,
cascade mode or attributes — so the whole state is read through the route and
the marker through either.

Clearing was refused on the node that never failed (409, naming the
condition) and succeeded on the failed one through `rimsky admin reset`. The
same read afterwards reported the node with no settled signal at all, while
its id, its executor, its run tallies and the check's findings were
byte-for-byte what they had been — the failure marker is gone and nothing else
about the node's readable state moved. The clearing itself appears in the
instance's event log as one operator-override entry against that node.

RESULT: PASS
