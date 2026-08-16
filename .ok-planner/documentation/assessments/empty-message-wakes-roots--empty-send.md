---
assessment: empty-message-wakes-roots--empty-send
subject: story:empty-message-wakes-roots
way: empty-send
release: d977250c
outcome: held
warrant: experiment:empty-message-wakes-roots
---
# Starting the default work with a message that names nothing

A send through `catalog:http-routes/POST /v1/instances/{id}/messages` whose entire request body was empty — naming no message type and supplying no envelope fields — woke all three of the template's structural roots, each dispatching exactly once. The wake is targeted rather than indiscriminate: the node carrying a declared upstream was not woken directly, while the node downstream of a root still ran by cascade, so "every structural root" means the roots and not everything in the graph. The empty send uses the same path every typed send uses, which the run read back rather than assumed — the message is a row in the same ledger at `catalog:http-routes/GET /v1/instances/{id}/messages` that operators read for typed sends, it carries the empty type, and it opened a frame whose triggering message is that row. Nine checks, none failing. "Start the default work" is therefore one call an operator or publisher can make without crafting an envelope.

## Unverified remainder

None: the passing run demonstrates the way as promised.
