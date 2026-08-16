---
assessment: live-progress--per-node-progress
subject: story:live-progress
way: per-node-progress
release: d977250c
outcome: held
warrant: experiment:live-progress
---
# Seeing each node settle while the rest of the one-shot run is still going

The audit drove a one-shot run of two instances through `catalog:cli-verbs/rimsky compose run`, where one instance settles at once and the other waits on an upstream that sleeps for eight seconds; every progress line was stamped with the second it reached the operator. Both instances emitted per-node lifecycle lines as they happened. The fast instance's node outcome was on screen one second in — seven seconds before the slow upstream could answer at all — and its instance summary at two seconds, nine seconds before the command returned. The slow instance's node line and summary arrived at eleven seconds, when that work actually finished. A watcher therefore knows at any moment which work has settled and which is still outstanding, which is the separation between healthy work and a hang the story rests on. Four checks, none failing.

## Unverified remainder

Progress was observed on a two-instance run with one deliberately slow upstream. The way does not establish what the display does at large instance counts, nor that a genuinely hung run is distinguishable from a slow one by any signal other than the absence of further lines.
