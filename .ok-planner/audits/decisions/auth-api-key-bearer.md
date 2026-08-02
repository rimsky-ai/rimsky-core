---
audit: auth-api-key-bearer
artifact: decision:auth-api-key-bearer
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:16Z
---

# The control API authenticates callers by api-key bearer token, not sessions or signed tokens

Supported. `lib/control/controlapi/auth_middleware.go::resolveIdentity` reads only the `Authorization: Bearer <plaintext>` header, hashes and looks up the presented plaintext against the api-key ledger (`lib/foundation/auth/plaintext.go`, `persistence.APIKeyTable.GetByHash`), and stores no server-side session state — the request is stateless, re-validated from the database on every call. No JWT/OAuth verification library, no signed-claims parsing, and no cookie-based session handling exist anywhere in `lib/control/controlapi`; grants are read from the database per request as the decision's rationale claims, not decoded from token claims.
