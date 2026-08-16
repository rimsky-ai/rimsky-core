---
audit: dry-run
artifact: concept:dry-run
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:06:31Z
---

# Preview-without-commit: mode resolution, universal write coverage, and the audit row's record of intent

Supported. All five invariants hold, as does the mode-resolution rule the body states. The gate resolves the request flag and the matched grant entry the way the concept describes: an unparseable flag value is rejected before anything runs, the flag alone raises an execute grant to dry-run, a dry-run grant entry forces dry-run whether the flag is absent or explicitly false, and where several grant entries match the same action the loop keeps the more permissive one, so a coexisting execute entry lifts the floor. The universal — every write is previewable — is enforced structurally rather than by a runtime gate: a scenario test enumerates the twenty-three write-marked actions from the action registry itself, fails if any lacks a coverage descriptor or if a descriptor names a non-write action, then drives each under the flag, requires a preview envelope carrying the action's would-have key, and re-reads state to prove nothing changed. There is no carve-out — the three auth-surface writes are inside that twenty-three and each is asserted to leave its key usable. Reads honour the flag as a no-op and are recorded as having executed, while a write under dry-run is recorded as not executed; that distinction is one expression in the audit emitter, so it cannot drift per handler, and the resolved mode rides every access-attempt row. Validation runs faithfully under dry-run, with scenario coverage that template registration still fires the validation protocol's checks against advertising services before returning a preview. Because every mounted route is proved present in the registry and every write-marked action is proved previewable, no write can reach a mutation while resolved to dry-run.
