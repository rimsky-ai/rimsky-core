---
assessment: script-friendly-outcome--all-succeeded
subject: story:script-friendly-outcome
way: all-succeeded
release: d977250c
outcome: held
warrant: experiment:script-friendly-outcome
---
# The "everything succeeded" outcome class, branchable without parsing output

The audit drove `catalog:cli-verbs/rimsky compose run` on a manifest whose checks all pass, and read the result the way a surrounding script would: a shell branch on the exit status alone, with the transcript discarded outright so nothing could have been settled by reading output. The all-pass manifest returned the success class. The branch was taken on that status and nothing else, which is what makes the class usable by a script that does not know the product's log format. Three checks across this way and its siblings, none failing.

## Unverified remainder

One all-pass manifest was driven. The way does not establish the class for a manifest whose instances all succeed but whose run emits warnings.
