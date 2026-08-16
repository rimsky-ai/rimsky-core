---
audit: emit-work-completed
artifact: decision:emit-work-completed
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:10:16Z
---

# The terminal-application step appends a work-completed event pairing its work-started twin plus the terminal kind

Supported. The terminal-application step appends the event from its post-commit step, and the declared payload is precisely the work-started payload's two identifiers — supervisor and dispatch — plus a terminal-kind field, with the event row carrying the same node and instance identifiers the started half carries; the two payload declarations were read side by side. The kind is therefore emitted rather than declared-and-unemitted, which is the standing lie the decision exists to close. Pairing is exactly one-to-one per dispatch: the in-place retry loop re-dispatches under the same dispatch identifier and the completion emit is skipped on a retry iteration, so a run that retries emits one started and one completed rather than one and several, and a park emits no completion because the run re-enters and its eventual terminal emits the pairing half. All five of those cases carry scenario tests — the complete and errored pairings, the retry singleton, the liveness-recovery singleton, and the park exclusion — and each asserts the shared dispatch identifier, the shared supervisor identifier, the terminal kind, and the ordering.
