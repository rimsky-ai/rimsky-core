---
audit: debug-channel-gate-paused-or-breakpoint
artifact: decision:debug-channel-gate-paused-or-breakpoint
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:29:43Z
---

# The debug-override endpoint admits only a paused instance or one held at a breakpoint

Supported. The override handler opens a transaction, reads the instance, and admits the request on exactly two conditions: the instance-level pause flag is set, or the instance has an unresumed pause-mode breakpoint hit. Anything else returns a conflict response naming the two legal states. The breakpoint half is checked by a query that joins the hit to its node run and requires that run to be in an in-flight state, so a hit whose runner has already moved on does not open the channel — the "blocking a runner" qualifier the decision states; both persistence backends implement that query and it is covered by the shared conformance suite. The rejected condition is genuinely absent: nothing in the gate consults frames held by a parked node-run, and the gate has no third branch. Tests cover a healthy instance refused with a conflict, a paused instance accepted, a breakpoint-held instance accepted, and the gate sharing one transaction with the mutation it guards.
