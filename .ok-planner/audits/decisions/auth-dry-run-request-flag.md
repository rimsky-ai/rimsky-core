---
audit: auth-dry-run-request-flag
artifact: decision:auth-dry-run-request-flag
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:36:49Z
---

# A per-request dry-run flag exists alongside, not instead of, the identity-level mode

Supported. `gateByAction` (`lib/control/controlapi/auth_middleware.go`) parses a `?dry_run=true|false` query flag into `requestedMode` independently of grant resolution, and only the grant-floor check (mode:auth-dry-run-mode-floor-on-key) can override it — so a fully-privileged (execute-mode) caller gets a genuine one-off preview via the flag alone, without needing a second dry-run-scoped credential, which is exactly the second rejected alternative's stated gap. The first rejected alternative (separate validation verbs beside each write verb) is absent as a general pattern: dry-run coverage runs through the single `?dry_run=` flag on the real write route for all 23 `IsWrite` actions (verified under `story:dry-run-request-flag`) rather than through a parallel verb per write; the one narrower `template:validate` endpoint pre-exists for spec-only syntax checking and does not duplicate this per-write-verb pattern. `TestDryRun_AuthCreateMintsNoKey` and the coverage suite exercise the flag end-to-end on an execute-capable key.
