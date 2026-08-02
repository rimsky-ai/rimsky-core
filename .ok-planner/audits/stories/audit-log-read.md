---
audit: audit-log-read
artifact: story:audit-log-read
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:34Z
---

# Operator reads auth-relevant action audit

Supported. `GET /v1/audit` is gated on the `audit:read` permission and reads
exactly the 5 `auth.*` event kinds (access_attempted, access_denied,
key_created, key_revoked, key_rotated) via a fixed allowlist checked against
the full `auth.*` set the codebase declares under `lib/foundation/auth`,
covering every case the story names (creates, revokes, rotates, dry-run
access attempts, denied attempts). The handler exposes filters for actor
(key_id, key_name), action (exact and prefix), target (request path),
result (response status), mode, and time range (since/until), matching the
"who did what and when" claim; the kind, key_id/key_name/action/status/mode
filter fields are each exercised by dedicated conformance-suite assertions
against the persistence layer (`observability.go`), and route-level HTTP
tests cover the kind allowlist and rejection behavior. Dry-run-mode access
attempts and denied attempts are each separately unit-tested for correct
payload shape.
