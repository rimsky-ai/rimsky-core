---
assessment: anonymous-mode-bootstrap--closes-on-first-key
subject: story:anonymous-mode-bootstrap
way: closes-on-first-key
release: d977250c
outcome: held
warrant: experiment:anonymous-mode-bootstrap
---
# Minting the first admin key shuts the door behind it

`catalog:cli-verbs/rimsky auth init` minted the first admin key and printed its plaintext once. Re-sweeping the same 83 control-API routes with no credential then returned 401 on 82 of them. The one survivor is `catalog:http-routes/GET /v1/health`, the liveness probe, which stays open by design so a container orchestrator can still reach it. The close is immediate and needs no restart, no configuration edit and no second act by the operator: the mint is the switch.

## Unverified remainder

None: the passing run demonstrates the way as promised across the whole published route population.
