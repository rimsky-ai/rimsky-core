---
audit: one-message-per-frame
artifact: decision:one-message-per-frame
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:26:44Z
---

# Every frame carries at most one delivered message; N pending messages produce N frames

Supported. Frame opening picks pending messages with a window function ranked by receipt time and id, partitioned by instance, and keeps only the first row per instance, and it considers only instances with no unresolved frame — so one frame opens per instance per pass, on the oldest pending message, and the rest stay pending. A frame records exactly one triggering message id, and the delivery pass for a running frame resolves that single id, refuses an already-delivered or cancelled message, and returns at most one row; no path marks a second message against the same frame. An end-to-end scenario sends several messages to one instance and asserts the same number of distinct frames, the same number of distinct triggering messages, and a maximum of one message per frame. Checked both delivery entry points (frame opening and the running-frame sweep) and the coalesce queue mode, which cancels prior pending messages rather than bundling them.
