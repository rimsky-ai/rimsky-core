---
story: opaque-executor-scratch
---

# Opaque bytes carried across recovery re-dispatch of the same node-run

## Story

As an executor author, I can attach opaque bytes to a settling Outcome and observe them on the next dispatch's recovery of the same node-run, so I can carry in-flight state across the runtime's stale-recovery cycle without rimsky inspecting or modifying the bytes.
