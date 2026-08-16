---
assessment: single-process-all-in-one--memory-blob
subject: story:single-process-all-in-one
way: memory-blob
release: d977250c
outcome: held
warrant: experiment:single-process-all-in-one
---
# Large payloads work with no external storage service, because the roles share the process

The audit measured the in-memory payload store by round trip rather than by configuration acceptance. With that backend selected and a small spill threshold, a node carrying an 8700-byte payload ran to success and the whole payload read back through the control API — written by the supervisor role, read by the control-api role, out of one in-process store. The same configuration handed to a single-role container was refused at startup with an error naming the backend as development-only and naming the single-process mode it requires. The coupling is therefore enforced rather than assumed, and an operator gets end-to-end work from one container with no storage service beside it.

## Unverified remainder

None: the passing run demonstrates the way as promised.
