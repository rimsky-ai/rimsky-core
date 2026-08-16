---
assessment: peer-auth-mtls-mutual--one-change-authenticates
subject: story:peer-auth-mtls-mutual
way: one-change-authenticates
release: d977250c
outcome: held
warrant: experiment:peer-auth-mtls-mutual
---
# One configuration change makes internal connections mutually authenticated

The audit brought up a stack with `catalog:config-keys/peer_auth` set to mutual authentication and the CA key supplied through `catalog:env-vars/RIMSKY_CA_ENCRYPTION_KEY`. The control-API listener then refused plaintext and served a certificate issued by the deployment CA, verified against the root served at `catalog:http-routes/GET /v1/ca-root`. A bundled executor (`catalog:images/rimsky-executor-http-node`) brought up under the same setting proved the authentication mutual on its own listener across three handshake classes: a client presenting no certificate was refused, a client presenting a certificate from a freshly generated impostor CA was refused, and a client presenting a leaf the deployment itself had issued through `catalog:http-routes/POST /v1/enroll` completed the handshake and saw a deployment-signed server certificate. That executor's own configuration entry declares no transport setting, yet the deployment reported it as required — the single change defaulted it — and a node dispatched over that connection settled fresh.

## Unverified remainder

The universal over internal connections was demonstrated on two connection kinds, the control-API listener and a bundled executor leg. It was not enumerated over every peer kind a deployment can configure.
