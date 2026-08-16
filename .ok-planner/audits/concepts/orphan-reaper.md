---
audit: orphan-reaper
artifact: concept:orphan-reaper
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:57:08Z
---

# The three reclamation sweeps, their cutoffs, and their guards

Unsupported on the cutoff invariant; the rest holds. Three separate sweeps exist and are deliberately not unified: the node-run dispatch sweep, the frame engine's in-frame dispatch sweep, and the claim-handle sweep. The two dispatch sweeps release rather than delete — each clears the claimant and returns the row to stale, reclaimable by the next dispatcher pass — while the claim-handle sweep hard-deletes, and all three carry a claimant-guarded predicate so a live owner is never clobbered, the two release paths matching the recorded claimant and the delete matching the holding supervisor. The claim-handle sweep's candidate query admits only active rows past their expiry, which is both why it skips settled rows and why committed-durable rows survive to the retention sweep or the asset release; it calls no producer abandon verb anywhere, and the acquisition bail path is the one place that does, with a test for it. Parked rows are skipped because parking clears the claim, which drops the row out of the dispatch sweep's candidate query — proved by a conformance case run against both backends. The contradicted claim is the cutoff rule. The concept states that the node-run and frame-dispatch sweeps both key on the run's progress timestamp against per-dispatch quiet-period and absolute-runtime deadlines. The node-run sweep does exactly that, checking each deadline only when it is configured. The in-frame sweep does not: its candidate query selects runs that still carry a claim while their frame has already been marked ended, and its release consults no timestamp, no quiet period, and no runtime deadline. Its cutoff is structural — a claim outliving its frame — not the deadline pair the invariant names.
