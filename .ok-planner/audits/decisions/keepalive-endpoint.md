---
audit: keepalive-endpoint
artifact: decision:keepalive-endpoint
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Dedicated run-id-keyed keepalive route, cancel-token authenticated, no-content response, single side effect

Supported. `lib/runtime/keepalive.go` registers `POST /v1/runs/{run_id}/keepalive` as a route distinct from the attribute-writeback callback (`/v1/runs/{run_id}/attributes`, registered alongside it in `callback.go`); `handleKeepalive` authorizes the request with the dispatch's cancel token (`SupervisorID + ":" + runID` compared via `authorizeCancelToken`, the same helper the attribute-writeback handler uses), ignores the request body entirely, and its only persisted effect is `Queue.BumpLastProgressAt` plus a claim-expiry renewal — it never touches the attribute bag. Both the keepalive and attribute-writeback handlers answer with `http.StatusNoContent`, matching the "same no-content convention" claim. Twelve `TestKeepalive_*` cases in `lib/runtime/keepalive_test.go` cover invalid run id, missing/wrong/malformed bearer token, wrong-supervisor token, mTLS accept/reject paths, unknown run, bump failure, and successful bump using an injected clock.
