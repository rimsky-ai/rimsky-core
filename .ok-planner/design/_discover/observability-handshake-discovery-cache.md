---
topic: observability-handshake-discovery-cache
kind: schema
---

# Startup handshake probes each peer's observability endpoint in parallel; `Discovery` cache backs all `/observability/*` reads

## Description

Rimsky's control-api exposes observability data partly from rimsky's own state (event log, frame ledger, claim handles) and partly from peer-side capabilities (declared events, userdata schema, custom UI URLs, HTTP bridge URLs). Peer-side data is fetched once at startup via the optional observability protocols and cached in `Discovery`.

`modeling/observability/handshake.go::RunHandshake` (lines 174-196) dials each declared executor and producer endpoint at startup with a 30-second per-peer timeout (`handshakeTimeout` constant, line 22). Probes run in parallel per peer (one goroutine each) so total wall time is `~handshakeTimeout`, not `N × handshakeTimeout`. Each goroutine applies its own per-probe `context.WithTimeout`.

Per the spec §4 (referenced inline), the handshake is **best-effort**: unreachable peers or absent endpoints are recorded as `Unreachable` and never cause the function to return an error. `RunHandshake` always returns a populated `Discovery` cache.

`gRPCProber` (`handshake.go:34-37`) is the default `Prober`. It dials with insecure transport ("the operator-configured perimeter handles auth in v1") — a future v1 deployment would need TLS-mediated peers, and the prober interface is the seam.

The probe results (per-peer) populate `PeerEntry`:

- `Name`, `Endpoint`, `ObservabilityEndpoint` (the dialed URL).
- `LastProbedAt`, `LastError`.
- `Reachability` (`Reachable` | `Unreachable`).
- `Capabilities` — `ObservabilityCapabilities` for executors (with `declared_events`, `userdata_schema`, `CustomUI`, `HTTPBridgeURL`) or `StoreObservabilityCapabilities` for stores (with `admin_views`, claim-trace booleans).
- `HTTPBridgeURL` — denormalized for fast access.

The capability response objects deep-copy the `userdata_schema` bytes (`handshake.go:74-78`) because the proto's underlying slice may be reused by gRPC; the cache needs a stable snapshot.

`RefreshLoop` (lines 259-336) re-probes every peer at a configurable interval (default 60s). Heals transient unreachability per spec §4. Each refresh runs the same parallel-probe pattern and replaces the cache entry.

The cache backs:

- `GET /executors` / `GET /executors/{name}` — list/get from cache.
- `GET /stores` / `GET /stores/{name}` — same.
- The `declared_events` cross-check at template registration (`docs/concepts/handlers.md` "Validation at template registration").
- The `userdata_schema` validation at dispatch (`modeling/observability/userdata_validator.go`).
- The dashboard's `http_bridge_url` resolution.

A peer that's unreachable at startup has no `Capabilities`; the `declared_events` cross-check is skipped silently (CLAUDE.md "Non-obvious gotchas"); the runtime treats unknown event names as no-ops.

`chooseObsEndpoint` (line 338-343) implements a small precedence: explicit `observability_endpoint` from rimsky.yml wins; falls back to the peer's `endpoint`. `stripScheme` (line 146-152) strips `grpc://`, `http://`, `https://` prefixes (operators historically use `grpc://host:port`).

## Code surface

- `modeling/observability/handshake.go` — entire file (~340 lines).
- `modeling/observability/discovery.go` — in-memory cache + `PeerEntry` struct.
- `modeling/observability/userdata_validator.go` — uses cached `userdata_schema`.
- `modeling/observability/handler.go` — HTTP handlers serving cached data.
- `cmd/rimsky-control-api/main.go` — calls `RunHandshake` at startup and spins `RefreshLoop` as a goroutine.

## Prose surface

- `docs/concepts/operational-health.md` — observability handshake briefly.
- `CLAUDE.md` "Non-obvious gotchas" — `declared_events` cross-check skipped silently.

## Adjacent topics

- `2026-05-10-observability-optional-protocols` — protocol-level shape.
- `named-events-and-on-event-handlers` — `declared_events` consumer.
- `2026-05-10-attribute-substitution-grammar` — userdata-schema validation site.

## Observations

- The handshake is best-effort by design but has consequences: a peer that's down at startup is silently de-validated for template registration. Operators who deploy a new executor template before the executor is up will get a successful registration that has skipped a check.
- Insecure transport at v1 is operator-acknowledged ("the operator-configured perimeter handles auth"). A future v1 release with TLS-required would need a `tls` config block in `rimsky.yml` per-peer and a `tls.Config` plumbing through `dial`.
- The refresh interval defaults to 60s; transient unreachability heals within that window. A peer with persistent issues (e.g. listening on the wrong port) cycles between Reachable / Unreachable as the refresh runs, producing churn in the observability log.
- The cache is in-memory per control-api process. A multi-replica control-api deployment has multiple caches that probe independently — different replicas may see different observed states briefly.
- The dial function uses `grpc.NewClient` (post-1.65) which is lazy; the actual reachability gate is the `Capabilities()` call bound by `ctx`. So a peer that resolves but doesn't answer becomes `Unreachable` only at the timeout — not at dial.
