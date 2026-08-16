---
assessment: validation-warnings-surfaced--advisories-on-register
subject: story:validation-warnings-surfaced
way: advisories-on-register
release: d977250c
outcome: held
warrant: experiment:validation-warnings-surfaced
---
# The advisories come back on a successful registration too

Registration of the same template succeeded and carried the advisory on its answer, so an author who registers without checking first still gets the advice rather than losing it. The advisory did not block: the template registered, as an advisory should allow.

## Unverified remainder

One gap sits here: a successful registration through `catalog:cli-verbs/rimsky template register` prints only the template id and drops the advisories the answer it read carried. An author reaching for them through `catalog:cli-verbs/rimsky template lint` or the registration route itself still gets them, but an author who only registers from the CLI does not see them.
