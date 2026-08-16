---
assessment: validation-warnings-surfaced--promote-to-blocking
subject: story:validation-warnings-surfaced
way: promote-to-blocking
release: d977250c
outcome: held
warrant: experiment:validation-warnings-surfaced
---
# Making advisories blocking, so questionable declarations cannot register

With `catalog:cli-flags/--warnings-as-errors`, the audit found the verdict flipped on both paths. Validation answered not-ok and still named the advisory that flipped it; registration was rejected, echoed the setting, named the advisory, and persisted no template — the catalogue count was unchanged either side. The same two paths through the operator CLI behaved the same way. An author or a pipeline can therefore treat advice as a gate and fix questionable declarations before running the template.

## Unverified remainder

None: the passing run demonstrates the way as promised.
