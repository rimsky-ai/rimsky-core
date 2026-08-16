---
assessment: one-message-per-frame--backlog-burst
subject: story:one-message-per-frame
way: backlog-burst
release: d977250c
outcome: held
warrant: experiment:one-message-per-frame
---
# One message per frame when a burst backs up behind a busy instance

The audit measured this where the promise would break if it were going to. Three distinctly-labelled messages arrived through `catalog:http-routes/POST /v1/instances/{id}/messages` while the instance was held busy, so the queue genuinely backed up rather than being delivered one at a time by luck. Each delivered message named a distinct frame, each frame's triggering message was one of the three, and the reacting node resolved exactly one body per run in arrival order, with no run recording a resolution failure. Substitution from the message body is therefore always well-defined in a node reacting to a message — no frame ever carried two bodies for the node to choose between. Eight checks across this way and its sibling, none failing.

## Unverified remainder

The burst was three messages of one declared type. The way does not establish the shape at much larger bursts or across several message types arriving together.
