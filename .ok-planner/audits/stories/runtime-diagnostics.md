---
audit: runtime-diagnostics
artifact: story:runtime-diagnostics
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:07:00Z
checked: 4
unaccounted: 0
---

# The four wedge diagnostics an operator can read off a stuck instance

Supported across all four members of the population the story names — parked
nodes, pending wake dependencies, held frames, and current claim holders. An
instance was wedged deliberately rather than simulated: a claim-holding node
parked and did not return, a second node co-held its claim, and a receiver
declared a force-refreshed dependency on the parked node. Each diagnostic was
then read back and cross-checked against the others rather than merely returning
rows. The park roster named exactly the one wedged node, identified it as the
claim holder, and carried both when it parked and when it is due back, with the
CLI returning the same row for the same node. The held-frame roster reported one
frame naming that node, its state as parked, and how long it had been held, and
that frame id appears on the instance's own frame listing. The frame's wait-set
carried three edges, every one naming a sender run, a receiver run, and what it
waits for, while the receiver had not run at all — and asked without a frame the
route refused with a 400 rather than guessing. The claim's holder list named one
holder, the parked node's run, still active, which is the reason the claim has
not come back. Every answer came through the control API or the CLI; the store
was never opened, which is the "without database spelunking" half of the promise.
Twenty-three checks, none failing.
