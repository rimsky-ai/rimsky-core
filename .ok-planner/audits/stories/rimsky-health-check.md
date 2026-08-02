---
audit: rimsky-health-check
artifact: story:rimsky-health-check
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:34:08Z
---

# Health probe surface for LBs and k8s

Supported. `lib/control/controlapi/app.go` registers `GET /v1/health` (`registerHealthRoutes`) before the `v1.Group` that applies `deps.AuthState.IdentityResolver()`, so the route is reachable with no bearer token even after anonymous mode is closed. `handleHealth` (`lib/control/controlapi/health.go`) runs a persistence transaction and returns `200 {"status":"ok",...}` on success; on any persistence error it falls through `writeError`'s default case to `500`, i.e. a non-2xx status. The CLI's `rimsky health` verb (`cmd/rimsky/cli/health.go`, `client_health.go`) calls the identical `GET /v1/health` endpoint. `test/scenarios/health_check_e2e_test.go`, tagged `@story: rimsky-health-check`, directly falsifies all three story clauses against a live stack: a healthy baseline returns 2xx with `status: "ok"`; the probe still returns 2xx after an admin key is minted (anonymous-after-mint, i.e. unauthenticated); and after the persistence driver's connection is severed the probe returns a non-2xx status. `lib/control/controlapi/health_test.go` additionally checks the node-count body across all 7 `cascade.NodeState` values.
