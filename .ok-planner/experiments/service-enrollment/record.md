---
experiment: service-enrollment
commit: PENDING
---

# One credential per standing service

## What it ran against

A `rimsky-all-in-one` stack under `peer_auth: mtls` from the tree's own image
tag, locked down with an admin key. The standing service is the third-party peer
built for permissive-peer-build, running in an alpine container with nothing but
its api-key, the control-API URL and the deployment CA root in its environment.
Handshakes are probed from the host with openssl, using a client leaf obtained
from the ruled enrollment route. Re-run unchanged at this tree.

## What was observed

The service's key was minted through `rimsky auth create-key --role-file` with a
grant of exactly one action, and it read back that way. That key could enroll
(200) and could do nothing else: instances 403, key minting 403, audit 403.

Holding only that key, the service came up serving a certificate issued by
`CN=rimsky-deployment-ca` whose subject is the id of the key it enrolled with,
and its listener refused a caller presenting no certificate. The stack drove a
node through it to fresh, so the credential it obtained at startup is the one
carrying real work.

Issuance from that key repeats with no operator action: a second enrollment
returned a different certificate, and the issued credential expires in about 23
hours, so it has to be renewed to keep working. Restarting the service — the
operator touching nothing — brought it back serving a credential from the same
issuer. What this run does not do is elapse the renewal deadline of a live
certificate; the issuance the renewal loop performs is what was exercised, not
the wait that triggers it.

Revoking the one key stopped issuance: the enrollment route answered the revoked
key 401 with `revoked_token`, and a service restarted on it failed closed,
exiting with "initial enroll ... failed (fail-closed)" rather than serving
without credentials.

EXPERIMENT PASS
