---
assessment: frame-origin-audit--cascade-sent-message
subject: story:frame-origin-audit
way: cascade-sent-message
release: d977250c
outcome: held
warrant: experiment:frame-origin-audit
---
# Seeing that a frame opened because the instance itself sent a message

A node declaring `catalog:template-keys/nodes[].sends_message` sent a message of its own when the woken node settled, and the frame that message opened named its triggering message id, type, sender and sender kind, with the sending instance's id as the sender. Reading the frame on its own returned the same trigger the list gave, and the triggering message id resolved through `catalog:http-routes/GET /v1/messages/{id}` to a message whose type and sender kind matched. All three frames in the instance carried a trigger, and the three sender kinds were exactly the three origins the promise enumerates.

## Unverified remainder

One naming difference: the promise calls this origin a cascade-sent message, and the product reports its sender kind as the instance. The frame is attributed to the sending instance, so an operator reading the surface sees the instance's own id rather than the word the promise uses.
