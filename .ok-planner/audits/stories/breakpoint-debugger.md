---
audit: breakpoint-debugger
artifact: story:breakpoint-debugger
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:51:57Z
---

# An operator installs a breakpoint, reads its hits, resumes with an overlay, and deletes it

Supported. Driven through the public surface against a container of the released
all-in-one image, on a template whose worker node subscribes to a counter and
reads its count, with one pause-mode pre-dispatch breakpoint installed on the
worker. Fourteen checks, none failing. The breakpoint installed and read back
off the instance; the worker's first dispatch stopped at it, with the hits ledger
carrying one hit naming the worker's node and the sealed dispatch bag while the
held run read as running, and the unified event log carrying the same hit as one
record naming the breakpoint and the node — both places the story names.
Resuming the hit with an attribute overlay reported it the first resume, and the
re-fired dispatch settled carrying the overlaid value with nothing the overlay
did not name changed. Deleting the breakpoint answered no-content, removed it
from the instance and emptied the hits ledger, while the event-log record of the
hit survived the deletion.

## Compliance

- The body names an internal component and its mechanism — "an attribute overlay that the supervisor applies on re-fire" prescribes which part of the product applies it; the compliant clause is that the overlay applies when the dispatch re-fires.
- The body enumerates the delivery surfaces — "both on the unified event log and on the breakpoint-hits ledger" is where the product exposes hits, which decisions own; the compliant capability is seeing the hits the breakpoint takes.
