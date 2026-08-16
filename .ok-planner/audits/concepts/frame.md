---
audit: frame
artifact: concept:frame
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:57:08Z
---

# The frame as one cascade resolution: its row shape, its isolation, and its twelve invariants

Supported. The frame row carries a non-null triggering-message reference and a non-null root run-scope reference, both foreign-keyed, and the root scope is created in the same transaction as the frame row and before it, so the reference can never dangle. The stored state column is gone: lifecycle is derived at read time by one shared SQL expression that returns running when the end mark is unset, failed when any owned run settled failed for a reason other than the instance kill, terminated when a kill-signalled failure is present, and completed otherwise — the four values the concept names, with no queued state anywhere and every frame running from the pickup moment. Serial-per-instance is a partial unique index over instances with no end mark, not a convention, and arrival order comes from the pending-message query's received-at ordering; a scenario suite of a dozen cases covers frame start atomicity, the in-flight frame blocking the next serial queue, per-instance ordering, non-null frame references on in-flight dispatches, and frame end after an async callback. The five in-flight run states hold the frame open uniformly through one shared state list; the held-frames diagnostic query filters to the parked subset only, as stated. The end mark is written once by the frame-end reaper or by the terminate transaction itself, and the only other write to the row is the last-progress heartbeat, which the state-transition path refreshes for the run's own frame and only while that frame is still open. Retention prune captures each pruned frame's root scope and deletes it after the frame delete, leaving no scope behind. Frame isolation holds where it is checkable: the attribute diff-gate baseline is looked up within the run's own run scope, which never spans a frame, and falls back to the template's schema defaults rather than a prior frame's row when no in-scope predecessor exists; the node identity row lost its per-frame pointer and its update stamp in a migration, and no update statement against that table exists in either backend; and the instance row has exactly two mutators, both operator lifecycle actions outside frame processing.
