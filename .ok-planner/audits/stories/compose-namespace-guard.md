---
audit: compose-namespace-guard
artifact: story:compose-namespace-guard
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:22Z
---

# Server-side rejection of foreign `compose:`-prefixed writes

Supported. All 3 server write endpoints capable of introducing a new `compose:`-prefixed name — tag create, template-register-with-tag, and instance-create — reject a caller-supplied `compose:`-prefixed value unless the request both carries the compose-origin header and the caller's identity holds the dedicated `compose:origin` permission; a rejected write persists nothing (verified by a follow-up read). The guard is exercised directly at the HTTP layer (bypassing the CLI) as well as through the CLI's own `compose up`, and a permission-matrix e2e test confirms the header alone (without the grant) does not bypass the guard, an unmarked request is rejected identically, and only a bearer actually holding `compose:origin` is admitted for both a tag and an instance write — so the refusal is a server property, not something a well-behaved client surface merely chooses to honor.
