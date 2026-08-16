---
assessment: rules-doc-accuracy--cited-paths-resolve
subject: story:rules-doc-accuracy
way: cited-paths-resolve
release: d977250c
outcome: held
warrant: experiment:rules-doc-accuracy
---
# Every location the contributor rules cite is a real one

The audit took the project's contributor verification rules as a contributor actually receives them — from the shipped source tree, tracked, so the copy measured is the copy every checkout carries — and resolved what they cite. Ten citations were in a recognisable location shape, and all ten resolved against the checkout; the population is non-empty, so the check has something to be wrong about rather than passing vacuously. The four build steps the rules name are all declared in the project's build definition, and the rules name the image-rebuild step the verification sequence depends on. A contributor acting on the documented steps therefore reaches real surfaces. Six checks across this way and its sibling, none failing.

## Unverified remainder

The check covers citations written in a recognisable location shape. Prose references that name a surface without writing it in that shape are outside the population measured, and whether each documented step is correct — as distinct from resolving — is not settled here.
