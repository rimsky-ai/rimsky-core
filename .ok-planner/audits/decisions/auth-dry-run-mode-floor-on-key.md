---
audit: auth-dry-run-mode-floor-on-key
artifact: decision:auth-dry-run-mode-floor-on-key
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:24:31Z
---

# A dry-run grant pins its key to dry-run whatever the request asks for

Supported. The action gate computes the requested mode from the request flag and then floors it: when the grant that allowed the action resolves to dry-run mode, the effective mode is dry-run regardless of what the request asked, so no request flag can escalate an attempt-only credential to a real write. The grant-check function itself defaults an unset mode to execute and, where several entries of one key match the same action and target, prefers execute over dry-run — a resolution rule among the key's own grants, not a request override, and covered by order-independent tests. The floor is exercised end to end by a test that mints a key whose tag-create grant is dry-run, calls the write with no dry-run flag, and asserts both the dry-run response envelope and that no tag was created; a unit test covers the floor, the explicit-execute case, and the unset-mode default.
