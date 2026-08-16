---
assessment: anonymous-mode-bootstrap--open-before-first-key
subject: story:anonymous-mode-bootstrap
way: open-before-first-key
release: d977250c
outcome: held
warrant: experiment:anonymous-mode-bootstrap
---
# A fresh deployment answers every operator action with no credential presented

The audit brought up two fresh deployments of `catalog:images/rimsky-all-in-one` — one on the zero-config default, one configured for mTLS peer auth so the enrollment and CA-root routes are mounted — and swept all 85 control-API routes the release publishes, 83 against the default deployment and the two CA-gated ones against the mTLS deployment. With no credential presented, none of the 83 was refused: 34 answered 2xx, 12 answered 400 for deliberately empty bodies, and 37 answered 404 for deliberately absent identifiers. A complete operator lifecycle — register a template, deploy it, create an instance, read it, terminate it — ran end to end unauthenticated. The single deliberate exception held: `catalog:http-routes/POST /v1/enroll` refused with 403 and a message naming the missing authenticated principal, while the CA root answered 200 to the same anonymous caller.

## Unverified remainder

None: the passing run demonstrates the way as promised across the whole published route population.
