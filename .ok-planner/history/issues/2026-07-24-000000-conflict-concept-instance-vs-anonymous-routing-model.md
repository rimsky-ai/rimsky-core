---
issue: conflict-concept-instance-vs-anonymous-routing-model
kind: audit
category: conflicting
artifacts:
  - concept:instance
  - concept:host-agent-proxy
  - concept:anonymous-mode
status: repaired
opened: 2026-07-24T00:00:00Z
---

# One design doc still described a routing model the system no longer uses

Question: does `concept:instance`'s claim that the creator's api-key linkage "is the routing key the host-agent-proxy uses" agree with how `concept:host-agent-proxy` and `concept:anonymous-mode` describe dispatch routing?

Rule that determined the fix: MECHANICAL-VS-JUDGMENT-RULE names exactly this shape as mechanical — "a stale sentence in one artifact aligned to the commitment the code and the counterpart artifact already agree on." `concept:host-agent-proxy` ("Routing is uniform: every dispatch resolves the serving agent by the instance's stamped routing identity... There is no special-case anonymous routing rule") and `concept:anonymous-mode` ("An instance created in anonymous mode... is stamped at creation time with the target anonymous agent's routing identity") already agree with each other and with the code on a model where a dedicated stamped routing-identity field, not the creator's api-key linkage, drives routing. `concept:instance`'s invariant was the sole holdout, asserting the api-key linkage itself is the routing key — false today, and it never mentioned the field that actually does the job.

What changed: in `.ok-planner/design/concepts/instance.md`, rewrote the invariant to state the creator's api-key linkage is retained for ownership/audit only (unchanged claim), and that routing uses a separate stamped routing identity resolved uniformly per `concept:host-agent-proxy` — matching the two counterpart artifacts. No change to the Boundaries "Owns" inventory (this corpus's ownership lists are not exhaustive field inventories, so silence there is not itself a defect).

How verified: re-read all three artifacts side by side to confirm the new wording states nothing beyond what `concept:host-agent-proxy` and `concept:anonymous-mode` already commit to; documentation-only change, no build/test surface affected.
