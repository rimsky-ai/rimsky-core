---
assessment: tag-management--list-and-resolve
subject: story:tag-management
way: list-and-resolve
release: d977250c
outcome: held
warrant: experiment:tag-management
---
# Listing the names and seeing what each one points at

The audit read the bindings back two ways: `catalog:cli-verbs/rimsky tag list` carried the name together with the hash it points at, and `catalog:cli-verbs/rimsky tag get` resolved the name to that same hash. An operator can therefore answer "what is this name currently deploying" from the deployment itself, which is what makes rolling forward or back a checkable act rather than a remembered one.

## Unverified remainder

One name was listed and resolved. The demonstration does not establish the listing's behaviour at scale or its ordering across many names.
