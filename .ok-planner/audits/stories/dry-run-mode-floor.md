---
audit: dry-run-mode-floor
artifact: story:dry-run-mode-floor
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:39:20Z
---

# An operator mints a key whose grant pins a write to dry-run, and the holder cannot escalate it

Supported. Three keys were minted through the public key-creation verb against a
fresh all-in-one deployment with authentication enabled: one granting a tag
creation pinned to dry-run mode, one granting the same action unpinned, and one
holding both the pinned grant and a wildcard grant covering the same action. The
pinned key's plain creation request — carrying no mode flag at all — returned a
synthetic would-have-created envelope and left nothing in the store; repeating it
with the request's own dry-run flag set to false produced the same envelope and
the same absence, so the holder cannot lift its own floor. The unpinned control
key performed the real write and its tag persisted. The mixed key also performed
the real write, which is exactly the proviso the story states: the floor holds
only while no other grant authorizes execute mode on that action. The pinned key
retained its read grant throughout.
