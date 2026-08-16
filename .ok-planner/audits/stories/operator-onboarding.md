---
audit: operator-onboarding
artifact: story:operator-onboarding
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:07:00Z
---

# A newcomer copies the shipped example and drives the dev loop to completion

Supported: everything the story tells a newcomer to do was done in order against
a running deployment, from the copy onward. The tree ships both the example
workflow and the two-verb walkthrough script beside it, and the README's
first-steps section names the template by the path it actually occupies. Copied
out of the tree into a scratch directory — so nothing depended on running inside
the repo — the example took one run verb, which exited zero and printed the
instance id the walkthrough tells the operator to carry forward; the watch verb
then returned zero once the instance reached terminal, and the instance settled
at success. The shipped walkthrough script, run from the same copy, went end to
end on its own and printed its completion line. Eleven checks, none failing.

## Compliance

- The body names the delivery surface ("run a single CLI verb"), which the story
  rules place in decisions; the compliant text names the outcome only, e.g. "I can
  copy a shipped example workflow, start it against my local deployment in one
  step, and watch it run to completion, so that I learn the dev loop end-to-end
  without writing a template from scratch."

## Referrals

- referral: that a newcomer with no prior experience learns the dev loop end-to-end from the shipped material
  established: the tree ships the example workflow and a walkthrough script beside it, the README's first-steps section names the template by path, and the walkthrough runs unedited from a copy to its own completion line
  discipline: documentation
