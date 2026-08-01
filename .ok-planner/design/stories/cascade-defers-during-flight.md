---
story: cascade-defers-during-flight
status: as-is
---

# In-flight node-runs are sealed against cascade-driven invalidation

## Story

As a template author whose executor relies on a fixed view of its inputs across the dispatch arc, I can know that once my node-run is dispatched, no upstream event can re-invalidate it or rewrite its substituted attribute bag. Upstream cascades that arrive during my run produce a NEW node-run that the system queues and dispatches after the current one settles. The one sanctioned state effect an upstream cascade has on my in-flight run: if my run is parked, the cascade wakes it early (parked to stale, same bag, same scratch) so it resumes promptly instead of sleeping until its resume-at while the queued cascade round waits behind it.

A node-run in any in-flight state — `pending`, `stale`, `running`, `held`, `parked` — is sealed against re-invalidation and bag rewrite: the cascade walker never rewrites an in-flight run's bag, scratch, or identity. When a cascade walk targets a receiver that already has an in-flight run, the walker creates a new cascade-driven node-run (subject to the per-template `cascade_mode`) and records the cascade against the new run's wait-set. The new run waits in line via the dispatcher's serialization gate (no claim while another run for the same (node, run-scope) is in running/held/parked). A parked receiver is the one carve-out: the walk additionally wakes it (parked to stale, state-only, through the single parked-wake path) so the queued round is not blocked on a sleeping run.

Without this guarantee, an executor cannot rely on the inputs it received at dispatch — they could be rewritten under it by an upstream re-run, breaking continuation primitives (parking, held-claim work, long-running async-callback flows) and any executor that reads its inputs more than once. With this guarantee, "this is a single dispatch" means what an executor author expects: one bag in, one outcome out, no retroactive mutation.
