---
audit: deposit-settle-window
artifact: decision:deposit-settle-window
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# An optional per-watch modification-time quiescence window holds mid-write deposits

Supported. The object-store sensor's subscription config accepts a settle-window duration per watch, rejects a negative one at subscribe time, and leaves it zero when absent, which is the off default. The poll loop applies it before anything else it does with a listed object: when the window is positive and the object's modification time is within it of the poll's clock, the object is skipped outright — not published, and, because the skip happens before the seen-marking step, not recorded, so every later poll reconsiders it and publishes once it has been quiet for the full window, using the metadata from that poll's fresh listing rather than any earlier one. The check sits in the shared poll path above the backend lister interface, so it is backend-agnostic exactly as claimed and applies identically to the filesystem and in-memory listers the sensor registers. A test writes a settled file and a mid-write file to a real temporary directory, drives three ticks across a pinned clock, and asserts the mid-write file is withheld on the first two, is published on the third, and carries the final content's digest.
