---
assessment: node-admin--read-node-state
subject: story:node-admin
way: read-node-state
release: d977250c
outcome: held
warrant: experiment:node-admin
---
# Reading a node's whole state on a running instance

The audit paired a released stack with the bundled shape-check executor service (`catalog:images/rimsky-executor-verifier-shape-checks`) on a template whose two nodes share one declared check and are fed clean and violating rows, so one instance settles with one node successful and one failed. Reading the failed node through `catalog:http-routes/GET /v1/nodes/{id}` returned its whole state in one document of ten fields — identity, instance, node type, executor, declared tags, cascade mode, creation time, run tallies, the attributes the run left behind including the offending row and the check that rejected it, and the settled failure signal. The healthy node's identical read carried a success signal instead, so the marker distinguishes the two rather than being decoration. `catalog:cli-verbs/rimsky node get` renders a narrower seven-field projection of the same document, which still carries the marker. Twenty-seven checks across this way and its sibling, none failing.

## Unverified remainder

The read was taken on two nodes of one template against one bundled executor. The operator CLI's projection omits the declared tags, cascade mode and attributes, so an operator wanting the whole state reads it through the route.
