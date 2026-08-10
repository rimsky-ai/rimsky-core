---
audit: runtime-diagnostics
artifact: story:runtime-diagnostics
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# A deliberately wedged instance answers all four questions through the product

Supported. An instance was wedged on purpose — a claim-holding node parked and
did not return, a second node co-held its claim, and a receiver declared a
force-refreshed dependency on the parked node — and each of the four things the
story names came back without opening the store. The park roster named exactly
that node, with when it parked and when it is due back, and `rimsky parked list`
returned the same row. The held-frame roster reported one frame for the
instance, naming the parked node, its state, and how long the frame has been
held, and the same frame appears on the instance's frame listing. That frame's
wait-set carried three sender/receiver edges, each naming both runs and what it
waits for, while the receiver had not run — and the route refused a request with
no frame rather than guessing. The claim's holder list named one active holder,
the parked node's run.
