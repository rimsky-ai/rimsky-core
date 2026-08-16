---
assessment: frame-origin-audit--operator-message
subject: story:frame-origin-audit
way: operator-message
release: d977250c
outcome: held
warrant: experiment:frame-origin-audit
---
# Seeing that a frame opened because an operator posted a message

One instance was made to produce all three trigger kinds, and the frame an operator's posted message opened named its triggering message id, the message type, the sender and the sender kind, which read as the operator. Reading that frame on its own through `catalog:http-routes/GET /v1/instances/{id}/frames/{frame_id}` returned the same trigger the list at `catalog:http-routes/GET /v1/instances/{id}/frames` gave. The triggering message id resolved through `catalog:http-routes/GET /v1/messages/{id}` to a message whose type and sender kind matched what the frame reported, so the two records agree. Narrowing the frame list by that triggering message id returned that one frame. Ten checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
