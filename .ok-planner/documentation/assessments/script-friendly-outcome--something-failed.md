---
assessment: script-friendly-outcome--something-failed
subject: story:script-friendly-outcome
way: something-failed
release: d977250c
outcome: held
warrant: experiment:script-friendly-outcome
---
# The "something failed" outcome class

The same shell branch was run against a manifest mixing a passing check and a failing one, and it returned the failure class — distinct from the success class, on the exit status alone. A script can therefore tell a run that produced a real failure from one that did not, and fail rather than proceed, without inspecting any output.

## Unverified remainder

One mixed manifest, with one passing and one failing check, was driven. The way does not distinguish among kinds of failure within the class.
