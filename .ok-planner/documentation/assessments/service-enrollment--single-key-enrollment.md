---
assessment: service-enrollment--single-key-enrollment
subject: story:service-enrollment
way: single-key-enrollment
release: d977250c
outcome: held
warrant: experiment:service-enrollment
---
# One api-key is the whole credential story for a standing service

The audit locked a deployment down under mutual-TLS peer auth (`catalog:env-vars/RIMSKY_PEER_AUTH`, `catalog:config-keys/peer_auth`) with an admin key, then minted the standing service one key through `catalog:cli-verbs/rimsky auth create-key` carrying exactly one action, `catalog:permission-actions/service:enroll`. That grant read back as the enrollment action alone, and the key was refused instances, key minting and the audit log. Holding only that key and the control-API address, the service came up serving a certificate issued by the deployment CA whose subject is the id of the key it enrolled with, and its own listener refused a caller presenting no certificate. The deployment then drove a node through that service to a fresh outcome, so the credential it obtained at startup is the one carrying real work — the operator wrote no certificate, key or trust store by hand.

## Unverified remainder

One standing service with one key was exercised. The demonstration does not establish what happens when two services enrol on the same key.
