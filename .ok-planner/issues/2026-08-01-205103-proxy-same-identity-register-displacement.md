---
issue: proxy-same-identity-register-displacement
kind: human
category: unspecified
artifacts:
  - concept:host-agent-proxy
  - concept:host-agent
status: open
opened: 2026-08-01T20:51:03Z
---

# Should the host-agent-proxy concept state the same-identity registration displacement rule (latest Register wins, prior connection closed, displacement flagged to the new agent)?

## Problem

The api-key intent dossier (2026-05-24, host-agent-and-proxy, artifact tier) records an affirmative displacement rule for agent registration: the latest Register under the same key wins, the older connection is gracefully closed, and the new registration's acknowledgment carries a displaced-prior flag. The code still holds this: the proxy displaces the prior connection and sets the flag (`code:cmd/rimsky-host-agent-proxy/agent_server.go`, `proto:host_agent.proto::RegisterAck.displaced_prior`) and the agent runtime reads and warns on it (`code:lib/runtime/hostagent/run.go`), so the keyed reconnect story (agent restart, stale-connection takeover) is protected only by code and tests. The live corpus only implies it negatively: `concept:host-agent-proxy` says an unauthenticated client "can neither displace a registered agent" and that displacement between two anonymous agents is "impossible by construction" — both presuppose that authenticated same-identity displacement exists, but no artifact states the affirmative rule (latest wins, prior closed, displacement surfaced to the incoming agent). Both `concept:host-agent-proxy` and `concept:host-agent` are sprint-final-form artifacts, so the gap cannot be repaired by this reconciliation pass and needs the owner's ruling on where (and whether) the commitment lands.

## Candidates

- Amend `concept:host-agent-proxy` (in a future sprint) with an invariant stating the same-routing-identity displacement rule: a new authenticated registration under an identity that already has a connected agent supersedes the prior connection, the prior connection is closed gracefully, and the new agent is informed it displaced a prior one.
- Record it as a decision (registration-conflict resolution: latest-wins vs reject-new) instead, leaving the sprint-final concepts untouched and citing the decision from code.
- Rule the behavior implementation detail below corpus altitude — the existing negative statements (unauthenticated cannot displace; anonymous cannot collide) are the only commitments; the latest-wins mechanics stay test-pinned only.
