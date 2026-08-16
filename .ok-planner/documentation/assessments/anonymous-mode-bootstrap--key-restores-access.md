---
assessment: anonymous-mode-bootstrap--key-restores-access
subject: story:anonymous-mode-bootstrap
way: key-restores-access
release: d977250c
outcome: held
warrant: experiment:anonymous-mode-bootstrap
---
# The minted key gets the operator back everything anonymous mode had

With anonymous mode closed, the freshly minted admin key restored every action the run checked: auth status reported the deployment authenticated, instance reads answered, and template registration succeeded. On the mTLS deployment, `catalog:http-routes/POST /v1/enroll` — the one route that had refused the anonymous caller — answered 200 for the key, and the CA root stayed open as before. Locking down therefore costs the operator nothing but the credential: the surface available before the mint is the surface available after it, presented with the key.

## Unverified remainder

None: the passing run demonstrates the way as promised.
