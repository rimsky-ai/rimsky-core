---
audit: cascade-defers-during-flight
artifact: story:cascade-defers-during-flight
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:30:00Z
---

# An in-flight node-run keeps the inputs it was dispatched with

Supported. The story makes two claims and each was measured on its own run. Held
at a pause-mode breakpoint, a node-run's dispatch bag carried the upstream value
of its own moment; an upstream cascade arriving during that run added a second
node-run row in `pending` and left the running row untouched; the held run then
settled on the value it was dispatched with, not the freshened one, and the
queued run dispatched only after that settlement, carrying the freshened value.
Parked instead of running, an `http-node` run that a 429 response parked with a
resume time an hour out was woken within a second by an upstream cascade, with
the wake recorded as `upstream_cascade`, and the woken work then settled. Both
runs drove a stack through the control API only.
