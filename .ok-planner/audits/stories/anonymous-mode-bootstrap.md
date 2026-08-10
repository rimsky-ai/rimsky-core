---
audit: anonymous-mode-bootstrap
artifact: story:anonymous-mode-bootstrap
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:45:00Z
---

# A fresh deployment is open, and the first admin key closes it

Supported, measured on a fresh zero-config stack and a second stack with a
deployment CA (the condition under which the enrollment route exists at all).
The population is the surface ruling's 88 public HTTP routes: 82 belong to the
control API, and every one of them was driven without a token. With no key
minted, none was refused — 31 answered 2xx, 12 answered 400 for deliberately
empty bodies, 37 answered 404 for deliberately absent identifiers — and a full
operator lifecycle (register, deploy, create, read, terminate) ran
unauthenticated end to end; the single refusal anywhere was machine service
enrollment, 403, exactly the exception the story names. Minting the first admin
key through the bootstrap verb closed the deployment: re-driving the same 80
routes unauthenticated returned 401 on 79, the one that still answers being the
liveness probe, and the minted key restored every action including the
enrollment route that had refused the anonymous caller.
