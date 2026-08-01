---
issue: conflict-scratch-mid-dispatch-channel
kind: audit
category: conflicting
artifacts:
  - decision:scratch-protocol
  - story:opaque-executor-scratch
status: answered
opened: 2026-07-25T21:11:30Z
---

# Does the opaque-executor-scratch story still describe a mid-dispatch scratch channel that `decision:scratch-protocol` says was never built?

No. `story:opaque-executor-scratch` has since been rewritten to the single agile-statement form (`As an executor author, I can attach opaque bytes to a settling Outcome and observe them on the next dispatch's recovery of the same node-run, so I can carry in-flight state across the runtime's stale-recovery cycle without rimsky inspecting or modifying the bytes.`) and no longer mentions a mid-dispatch scratch-callback channel at all — it names only the settling-outcome path, matching `decision:scratch-protocol`'s Choice ("There is no mid-dispatch scratch write channel..."). The filed conflict no longer exists.
