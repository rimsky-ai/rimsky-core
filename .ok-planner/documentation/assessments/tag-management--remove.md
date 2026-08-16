---
assessment: tag-management--remove
subject: story:tag-management
way: remove
release: d977250c
outcome: held
warrant: experiment:tag-management
---
# Removing a name that is no longer wanted

The audit removed the name with `catalog:cli-verbs/rimsky tag rm`. It left the listing and no longer resolved as a template reference, while the instances created under it kept running. Removing a deployable name is therefore a catalogue act, not a shutdown: the operator retires the name without touching the work already started through it.

## Unverified remainder

One removal was exercised. The demonstration does not establish what removing a name does to an instance created through it that has not yet started.
