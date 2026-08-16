---
audit: launch-integration
artifact: decision:launch-integration
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:29:43Z
---

# The compose verb mirroring, rather than sharing, the entrypoint's role orchestration

Unsupported, because the orchestration is shared rather than mirrored. Everything the Choice describes about behaviour is present: the same three role runners — scheduler, supervisor, control-api, in that order — back both launch sites, each runner's stop function is tracked, a role failure surfaces on a combined channel the caller selects on beside its signal channel, and shutdown drains the tracked stops in reverse. The process-role marker is set to the unified value by both the compose verb and the self-hosted run verb, and the memory blob backend's gate names exactly those two sites plus the entrypoint's no-command path as the ones permitted to select it. But the decision's substance is that the start, track, select and drain loop stays duplicated in a sibling site rather than being pulled into a shared helper, and it records that extraction as the rejected alternative. In the tree as it stands the extraction has happened: one exported launcher in the launch package starts the runners in order, accumulates their stop functions, owns the failure channel and drains in reverse, and both the all-in-one entrypoint and the compose verb call it. Only the select on signal-or-failure remains written out at each site. Three of the four steps the decision says are mirrored are in fact shared, so the artifact describes a shape the code no longer has.
