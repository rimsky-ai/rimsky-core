---
assessment: template-fan-out--concurrent-dispatch
subject: story:template-fan-out
way: concurrent-dispatch
release: d977250c
outcome: held
warrant: experiment:template-fan-out
---
# The partitions really do run at the same time

The audit pointed the fan-out's work units at an endpoint that holds each request open and reports the peak number in flight. The endpoint saw a peak of three in flight and three served, so the units genuinely overlapped. The same template with the parallelism knob set to one gave a peak of one and the same three served — the control that proves the overlap is the product's dispatch and not something the endpoint did.

## Unverified remainder

Two parallelism settings over three partitions were exercised. The demonstration does not establish behaviour when the declared partitions outnumber the parallelism budget by a large factor.
