---
assessment: peer-tls-enforced--credentialed-peer-satisfies
subject: story:peer-tls-enforced
way: credentialed-peer-satisfies
release: d977250c
outcome: held
warrant: experiment:peer-tls-enforced
---
# A peer that can present credentials satisfies the requirement rather than merely being refused

Enforcement is only half the promise; the setting also has to be satisfiable. Against a peer that can present credentials, the deployment reported the entry reachable at the required setting through `catalog:http-routes/GET /v1/observability/executors/{name}`. The certificate that peer presents verified against the deployment CA when probed from outside with a leaf issued by `catalog:http-routes/POST /v1/enroll` and checked against the root at `catalog:http-routes/GET /v1/ca-root`. A node driven over that connection settled fresh, carrying the credentialed peer's own writeback — so the operator gets a verified connection that does real work, not merely a connection that is not refused.

## Unverified remainder

One credentialed executor peer was driven. The way does not establish credential renewal or rotation on a long-lived connection.
