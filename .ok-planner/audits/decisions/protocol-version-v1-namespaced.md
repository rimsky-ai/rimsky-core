---
audit: protocol-version-v1-namespaced
artifact: decision:protocol-version-v1-namespaced
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:44:46Z
---

# The whole control-API contract surface sits under one v1 namespace

Supported. All 10 `lib/protocols/proto/v1/*.proto` files declare `package rimsky.v1;`. The control-API's chi router (`lib/control/controlapi/app.go`) mounts every one of its route groups — templates, tags, instances, breakpoints, debug overrides, nodes, events, audit, claims, messages, frames, assets, lineage, admin diagnostics, auth, enroll, MCP, and the observability sub-router — inside a single `r.Route("/v1", ...)` block, including the control-API's own `/v1/health`. The supervisor's separate async-callback listener (`lib/runtime/callback.go`), which the decision explicitly calls out as in-scope, likewise serves `/v1/callback/{async_ack_id}`, `/v1/runs/{run_id}/keepalive`, and `/v1/runs/{run_id}/attributes` — its own bare `/health` liveness probe is a peripheral infrastructure endpoint outside the contract surface, not a carved-out contract route. No bare-path contract route was found on either server.
