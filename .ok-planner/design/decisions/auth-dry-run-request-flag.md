---
decision: auth-dry-run-request-flag
status: as-is
---

# Per-request dry-run

## Choice

A per-request dry-run flag on writes.

## Rationale

Preview without persisting.

## Alternatives

- Separate validation verbs beside each write verb — rejected: doubles the surface and drifts from the real write path it is supposed to preview.
- Dry-run only as an identity-level mode (per `decision:auth-dry-run-mode-floor-on-key`) — rejected as the sole mechanism: fully-privileged callers also need one-off previews without a second credential.
