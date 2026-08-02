---
audit: in-place-retry
artifact: decision:in-place-retry
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:36Z
---

# Executor and acquire retries are both in-place on the existing node-run row

Supported. Both the executor-error retry path and the pre-dispatch acquire-error retry path resolve through one shared error-policy function; a retry decision stamps the same node-run row with a self-referencing prior-dispatch pointer and a retry-after-error disposition rather than transitioning its state or creating a new row, and the row's persisted retry counter is a single node-level value read and incremented by one shared evaluator regardless of which error class triggered the retry — no per-class counter, no cursor reset keyed on class. Release-and-requeue, the other policy outcome that keeps a dispatch alive, likewise updates the same row rather than replacing it. A dedicated singleton test drives a node through its full configured retry budget and confirms the run iterates in place (checked via the retry-signal count matching the configured budget) while the work-started/work-completed pairing for that run stays a single pair across every retry iteration, corroborating "no new node-run row is created" for the retry loop.
