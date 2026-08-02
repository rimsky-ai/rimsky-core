---
audit: inproc-eventstream
artifact: decision:inproc-eventstream
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095803-inproc-executor-client-goroutine-channel
---

# Unary in-process executor call

Unsupported. The in-process executor client's call remains unary, matching the protocol, but its implementation directly contradicts the decision's explicit mechanism claim: the call is bridged through a spawned goroutine and a buffered channel, selected against context cancellation, rather than running on the caller's own goroutine with no channel handoff as the decision states in as many words.
