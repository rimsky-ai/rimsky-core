---
assessment: script-friendly-outcome--bounded-out
subject: story:script-friendly-outcome
way: bounded-out
release: d977250c
outcome: held
warrant: experiment:script-friendly-outcome
---
# The "bounded out" outcome class, distinct from failure

The third class was provoked with a manifest whose node waits on a deliberately slow local server, run under a `catalog:cli-flags/--timeout` short enough to expire. It returned a class distinct from both success and failure. That distinctness is the whole point: a script escalates a run that ran out of time rather than treating it as a failed check. The three classes came back in the promised order with no two sharing a status, which is what makes them branchable at all.

## Unverified remainder

The bound was reached once, on one slow node. The way does not establish the class when several instances are outstanding at the moment the bound expires.
