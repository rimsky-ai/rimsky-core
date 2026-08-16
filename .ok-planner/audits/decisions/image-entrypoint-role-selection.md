---
audit: image-entrypoint-role-selection
artifact: decision:image-entrypoint-role-selection
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# Role selection from the entrypoint's command argument and the derivation of migration ownership

Supported. The entrypoint's role selector takes its argument list and returns all three roles for no argument, the named role for a single recognised one, and an error naming the offending value for anything else — an unrecognised name, the migrate binary's name, or more than one argument — after which the process exits non-zero. Migration ownership is derived rather than configured: the no-argument invocation owns it, a single-role invocation owns it only when the role is the control API, and every other single role skips it, so a three-container split runs migration exactly once. The owning invocation runs migration as a child process and blocks on its exit before starting anything, on both branches — the single-process all-in-one stack and the single-role child spawn — so no role ever starts against a half-migrated schema. An environment override can force or skip migration and rejects any other value at startup. The unit tests enumerate all eight selection cases, the default and overridden ownership rules, the three-container-split-migrates-once case, the launch plans for no argument and for each single role, and the single-role spawn actually starting only its own binary.
