---
assessment: validation-warnings-surfaced--advisories-on-validate
subject: story:validation-warnings-surfaced
way: advisories-on-validate
release: d977250c
outcome: held
warrant: experiment:validation-warnings-surfaced
---
# The author sees the validator's advisories while checking a template

The audit drove a template that trips exactly one advisory — an error-class policy naming a class no executor and no producer declares — and nothing else. `catalog:http-routes/POST /v1/templates/validate` answered ok and carried the advisory, and `catalog:cli-verbs/rimsky template lint` printed it. Advice the validator computes therefore reaches the author before they run anything, on both the route and the operator CLI.

## Unverified remainder

One advisory kind was exercised. The demonstration does not establish how a template tripping several different advisories presents them.
