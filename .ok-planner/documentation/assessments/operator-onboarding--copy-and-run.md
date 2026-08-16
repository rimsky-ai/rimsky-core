---
assessment: operator-onboarding--copy-and-run
subject: story:operator-onboarding
way: copy-and-run
release: d977250c
outcome: held
warrant: experiment:operator-onboarding
---
# A newcomer copies the shipped example and drives it to completion

Everything the story tells a newcomer to do was done in order against a running all-in-one deployment (`catalog:images/rimsky-all-in-one`), starting from the copy. The product ships both an example workflow and a two-verb walkthrough beside it, and the first-steps material names that example by the location it actually occupies. Copied out into a scratch directory — so nothing depended on running inside the project's own tree — the example took one invocation of `catalog:cli-verbs/rimsky run`, which exited zero and printed the instance id the walkthrough tells the operator to carry forward. `catalog:cli-verbs/rimsky watch` then returned zero once the instance reached a terminal state, and the instance settled successfully. The shipped walkthrough, run from the same copy, went end to end on its own and printed its completion line. Eleven checks, none failing.

## Unverified remainder

That a newcomer with no prior experience actually learns the dev loop from this material is a judgement the product's documentation discipline owns; what this run establishes is that the material exists, names the example correctly, and runs unedited from a copy to its own completion line.
