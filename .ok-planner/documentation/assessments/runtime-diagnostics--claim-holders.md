---
assessment: runtime-diagnostics--claim-holders
subject: story:runtime-diagnostics
way: claim-holders
release: d977250c
outcome: held
warrant: experiment:runtime-diagnostics
---
# Reading who currently holds a claim

Read through `catalog:http-routes/GET /v1/claim-handles/{claim_handle_id}/holders`, the claim's holder list named one holder — the parked node's run, still active — which is the reason the claim has not come back. Together with the park roster this closes the loop on the wedge: the same run is the parked node and the claim holder, so an operator can name the cause rather than the symptom. Every answer across all four diagnostics came through the control API or the CLI and the store was never opened, which is the "without ad-hoc database spelunking" half of the promise.

## Unverified remainder

One claim with one active holder was read. The way does not establish the holder list where a claim is co-held by several active runs.
