# Writing a Claim Producer

This guide is for developers who want to implement a new Rimsky claim producer — in any language — and wire it into a Rimsky deployment.

**Note on terminology:** the protocol-level term is **claim producer** (the service that implements `ClaimProducer` over gRPC). The colloquial term **store** survives at the bundled-services layer for data-backed reference impls (filesystem store, postgres store, stub store). This guide uses both.

The authoritative wire contract is `docs/specs/2026-05-04-service-protocol-contract.md` (see §2 for ClaimProducer specifically); this guide is the practical companion. For operator context, see `operator-guide.md`. For the conceptual model — claims, named locks, scope conflict — see `node-graph-design.md`. Vocabulary lives in `docs/glossary.md`.

External Go authors import only `github.com/fallguyconsulting/rimsky/protocols`:

```go
import (
    "github.com/fallguyconsulting/rimsky/protocols/claimproducer"
    // and if you also implement LifecycleSubscriber:
    "github.com/fallguyconsulting/rimsky/protocols/lifecycle"
    // proto bindings:
    genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)
```

The `protocols/` Go module has stdlib + grpc + protobuf dependencies only. You don't need to pull in `github.com/fallguyconsulting/rimsky/foundation` or the root `github.com/fallguyconsulting/rimsky` module.

> **Auth-blind advisory.** Rimsky has no machinery for credentials, encryption, or access control. Encrypt sensitive bytes before handing them to Rimsky if you need protection. Service-to-service auth is operator-configured at the deployment layer (mTLS, IAM).

---

## 1. The contract

A claim producer is a peer service. The Rimsky scheduler/supervisor/control-api processes dial it at startup, run a `Capabilities()` handshake, and issue four runtime verbs over gRPC:

```
service ClaimProducer {
  rpc Open(OpenRequest)             returns (ClaimResult);
  rpc Commit(ClaimVerbRequest)       returns (ClaimVerbAck);
  rpc Abandon(ClaimVerbRequest)      returns (ClaimVerbAck);
  rpc Release(ClaimVerbRequest)      returns (ClaimVerbAck);
  rpc Capabilities(CapabilitiesRequest) returns (CapabilitiesResult);
}
```

Source: `protocols/proto/v1/claim_producer.proto`. Go interface: `protocols/claimproducer/`.

That's the whole runtime contract. Rimsky owns the lock-state bookkeeping (`rimsky_claim_handle`), the orphan reaper, the state machine, and verb dispatch. You own the underlying state (filesystem paths, postgres rows, in-memory map) and the `Open`/`Commit`/`Abandon`/`Release` semantics.

### 1.1 Five obligations

Implementations MUST satisfy these (taken from the original v3 stores-redesign §7.8 obligations):

1. **Sweep / TTL for orphan reclamation.** Rimsky-side reaping covers the `rimsky_claim_handle` row. The producer-side state lives outside Rimsky's view; the producer must run its own TTL / sweep so partial-commits don't leak. The recommended TTL is **strictly greater than `5 × heartbeat_interval`** (Rimsky's orphan-reap window per blessed invariant 6) so a healthy producer doesn't strip a claim out from under a live supervisor.
2. **Record `claim_id` before any state mutation in `Open`.** `Open` is invoked inside Rimsky's atomic acquisition transaction (blessed invariant 15). If the producer mutates state but Rimsky's tx rolls back, the producer is left with orphan state. Recording `claim_id` first lets the producer's TTL sweep identify orphans.
3. **All terminal verbs idempotent in `claim_id`.** `Commit(claim_id)`, `Abandon(claim_id)`, `Release(claim_id)` MUST be safe to retry. Rimsky may retry on transient gRPC failures.
4. **Do not depend on Rimsky calling `Abandon` for orphan cleanup.** When Rimsky's orphan reaper deletes a stale `rimsky_claim_handle` row, **it does not fire any producer verb**. The producer's own TTL handles the cleanup.
5. **Canonicalize `scope` bytes such that byte-equal correctly indicates conflict.** Two `Open` calls that should conflict must produce byte-equal `scope` values. Rimsky's foundation compares byte-for-byte (`ScopesByteEqual`); no glob, no range-match, no canonicalization on the Rimsky side.

### 1.2 Byte-equal-scope uniformity

A second invariant on the producer: across the lifetime of the producer process, two `Open` calls returning byte-equal `scope` MUST return the same `realized_write_semantics`. Rimsky relies on this for the conflict predicate; producers enforce.

In practice: if your producer supports both `staged_async` and `sync` modes, it must consistently return the same one for any given canonical scope. The simplest path is "one mode per producer process" (single-element envelope); supporting per-claim variation requires per-scope state.

---

## 2. The Go interface

```go
package claimproducer

type ClaimProducer interface {
    Open(ctx context.Context, req OpenRequest) (ClaimResult, error)
    Commit(ctx context.Context, claimID uuid.UUID) error
    Abandon(ctx context.Context, claimID uuid.UUID) error
    Release(ctx context.Context, claimID uuid.UUID) error
    Capabilities(ctx context.Context) (CapabilitiesResult, error)
}

type WriteSemantics int

const (
    WriteSemanticsUnknown WriteSemantics = iota
    WriteSemanticsSync
    WriteSemanticsStagedAsync
    WriteSemanticsBlockingAsync
    WriteSemanticsReadOnly
)

type OpenRequest struct {
    ClaimID uuid.UUID
    Spec    json.RawMessage   // opaque to Rimsky
}

type ClaimResult struct {
    Address                json.RawMessage
    Payload                json.RawMessage
    Scope                  json.RawMessage   // canonicalized scope bytes
    RealizedWriteSemantics WriteSemantics    // per-claim
}

type CapabilitiesResult struct {
    WriteSemanticsEnvelope []WriteSemantics  // permissible values
}
```

### 2.1 `Open(ctx, req)`

Produce a producer-supplied address for the executor and register whatever producer-side state the `(intent × write_semantics)` combination requires (staging area, snapshot, MVCC transaction, or nothing). Inside the request:

- `claim_id` — Rimsky-generated UUID; record it before any state mutation (obligation 2).
- `spec` — opaque bytes from the rimsky-side template carrying selector / intent / alias / template-context. Producer parses; Rimsky doesn't classify or validate.

Return:

- `address` — producer-supplied bytes the executor uses to access claimed state.
- `payload` — producer-supplied data captured at acquisition (e.g., picked queue item's user data).
- `scope` — canonicalized scope bytes. Two acquisitions that should conflict must produce byte-equal `scope`.
- `realized_write_semantics` — the per-claim write-semantics value. MUST be a member of the producer-declared envelope from `Capabilities()`.

Resume detection is the producer's responsibility. The producer detects whether `Open` is a fresh acquisition or a resume (e.g., post-supervisor-crash) **internally by lookup against its own state, keyed by `claim_id`**. There is no `resumed` flag on `Open`; resume is a universal behaviour pattern, not a capability.

For pick-policy claims (selectors the producer recognizes as policy-form), `Open` invokes the configured pick policy and returns the picked item's address. The picked identifier becomes the canonical `scope` bytes.

### 2.2 `Commit(ctx, claim_id)`

Signals that the consumer of the claim succeeded. Producer disposition is per-producer config. Examples:

- For regional `rw` claims on `staged_*` mode: atomically publish the staging area's contents into live state.
- For `sync`-mode regional `rw` claims: producer-side no-op (writes already live).
- For pick-policy claims: apply the configured `on_commit_default` action (e.g., release_to_back, delete).

Idempotent in `claim_id` (obligation 3).

### 2.3 `Abandon(ctx, claim_id)`

Signals that the consumer of the claim failed. Producer disposition is per-producer config. Examples:

- For `staged_*` `rw` claims: discard the staging area without publishing.
- For pick-policy claims: apply the configured `on_give_up_default` action.
- For `sync` `rw` claims: degenerate (direct writes cannot be undone). Document this as an honest producer limitation in your README.

Not called for read-only claims (Rimsky calls `Release` instead).

Idempotent in `claim_id`.

### 2.4 `Release(ctx, claim_id)`

Tear down producer-side read state (snapshot, MVCC transaction) for a read claim. Fires only when the producer registered such state (relevant for `staged_async`; not exercised by any reference producer in the bundled set).

Idempotent in `claim_id`.

### 2.5 `Capabilities(ctx)`

Return the `WriteSemanticsEnvelope` — the set of `WriteSemantics` values this producer may return from `Open`. Probed once per peer at process startup; cached for the process's lifetime.

The operator declares a subset envelope per peer in `rimsky.yml` under `write_semantics_envelope:`. The capability handshake validates operator-declared ⊆ producer-declared; mismatch fails Rimsky startup.

### 2.6 Verb-firing matrix per claim shape

| Claim shape | `write_semantics` | Verbs invoked at terminal |
|---|---|---|
| Regional `r` | `sync` / `blocking_async` | None — claim_handle row deletion is sufficient |
| Regional `r` | `staged_async` | `Release(claim_id)` |
| Regional `rw` | `sync` | `Commit(claim_id)` (no-op) or `Abandon(claim_id)` (degenerate) |
| Regional `rw` | `staged_*` | `Commit(claim_id)` (atomic swap) or `Abandon(claim_id)` |
| Pick-policy claim | (any) | `Commit(claim_id)` or `Abandon(claim_id)` |

For **held claims**, Rimsky's auto-terminal mechanism (blessed invariant 13) fires exactly one resolution at holding-subgraph completion. Aggregate outcome — all-completed → `Commit`; any-failed → `Abandon` — drives the verb. From the producer's perspective, the verb call is indistinguishable from a non-held terminal.

---

## 3. Implementation pattern (Go reference)

The bundled stores under `stores/{filesystem,postgres,stub}/` are the reference Go implementations. They each ship as a standalone `package main` binary plus a Dockerfile and `config-example.yml`.

The skeleton:

```go
// stores/<kind>/store/server.go

import (
    genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
    "github.com/google/uuid"
    "google.golang.org/grpc"
)

type Server struct {
    genv1.UnimplementedClaimProducerServer
    cfg Config
    db  *sql.DB    // or filesystem handle, etc.
}

func (s *Server) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.ClaimResult, error) {
    claimID, err := uuid.Parse(req.GetClaimId())
    if err != nil { return nil, err }

    // 1. Record claim_id BEFORE any state mutation (obligation 2).
    if err := s.db.RecordClaimID(ctx, claimID); err != nil { return nil, err }

    // 2. Parse the opaque spec; resolve selector to a canonical scope.
    scope, address, payload, err := s.acquire(ctx, claimID, req.GetSpec())
    if err != nil { return nil, err }

    return &genv1.ClaimResult{
        ClaimId:                 claimID.String(),
        Address:                 address,
        Payload:                 payload,
        Scope:                   scope,    // canonicalized — byte-equal correctly indicates conflict
        RealizedWriteSemantics:  genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
    }, nil
}

func (s *Server) Commit(ctx context.Context, req *genv1.ClaimVerbRequest) (*genv1.ClaimVerbAck, error) {
    claimID, err := uuid.Parse(req.GetClaimId())
    if err != nil { return nil, err }
    if err := s.commitClaim(ctx, claimID); err != nil { return nil, err }   // idempotent in claim_id
    return &genv1.ClaimVerbAck{}, nil
}

// Abandon, Release, Capabilities — same shape.
```

Plus a sweep goroutine running at `sweep_interval_seconds` that scans the producer's own state for orphan claim_ids whose age exceeds `visibility_timeout_seconds` and reclaims them (obligation 1).

---

## 4. Implementation patterns by underlying tech

### 4.1 Filesystem (sync semantics)

The reference filesystem store is `stores/filesystem/`. Each `Open(spec)` resolves the selector to a concrete subpath under the configured `root:`; returns the absolute path as `address`. Concrete-paths-only — no glob support (canonicalization invariant).

For pick policies: folder auto-discovery under a sub-root. `mkdir`/`rm -rf` is the insertion/removal mechanism. Selectors of the form `@policy-name` invoke the configured pick logic.

`realized_write_semantics: sync` (writes happen in place, no staging).

### 4.2 Postgres (staged_async or items-table-queue semantics)

The reference postgres store is `stores/postgres/`. Each `Open(spec)` opens a transaction in the producer's own Postgres pool, mutates an items-table row to `'in_progress'` (recording `claim_id`), and returns the row's locator as `address`.

For pick policies: items-table queue with operator-declared `items_table:` per policy entry. A producer-internal sweep (`sweep_interval_seconds`) reverts `'in_progress'` rows older than `visibility_timeout_seconds` to `'available'`.

The producer's connection / DSN is its own — Rimsky never sees it. Operators may collocate it with Rimsky's control-plane database or point at a separate database.

`realized_write_semantics: staged_async` (the items-table queue makes reads and writes on different items independent).

### 4.3 In-memory (test fixture)

The reference stub store is `stores/stub/`. Used by scenario tests to simulate a configurable-region table + selector handlers without external dependencies.

`realized_write_semantics: sync`.

---

## 5. YAML config

### 5.1 In `rimsky.yml`

Operators declare your producer in the `claim_producers:` block:

```yaml
claim_producers:
  - name: my-store
    endpoint: "my-producer:7000"
    protocols: [claim_producer]              # default
    write_semantics_envelope: [staged_async] # operator-declared subset
    observability_endpoint: "my-producer:7001"   # optional
    http_bridge_url: "http://my-producer:7002"   # optional
```

If your producer also implements `LifecycleSubscriber`, declare it in the `protocols:` list:

```yaml
claim_producers:
  - name: my-store
    endpoint: "my-producer:7000"
    protocols: [claim_producer, lifecycle_subscriber]
    write_semantics_envelope: [staged_async]
```

There is no separate `lifecycle_subscribers:` block.

### 5.2 In your producer's own config

Your producer owns its own config schema; Rimsky never sees it. Conventionally a YAML file loaded from your binary's own env var (e.g. `STORE_<KIND>_CONFIG`). Reference shapes:

```yaml
# Your producer's own config — out of Rimsky's view.
host: 0.0.0.0
grpc_port: 7000
http_port: 7001
admin_port: 7002

# Your producer-specific configuration:
connection: "..."        # or root: /workspace, or whatever
write_semantics: staged_async
pick_policies:
  "@policy-1":
    items_table: items_p1
    on_commit_default: release_to_back
    on_give_up_default: release_to_back
    visibility_timeout_seconds: 300
sweep_interval_seconds: 30

# Optional: enable LifecycleSubscriber (see §6).
enable_lifecycle: true
```

Document your config schema in your producer's README.

---

## 6. Lifecycle subscriber (optional)

If your producer needs to react to control-plane lifecycle events (e.g. bootstrap a per-template schema on `OnTemplateDeployed`), implement the `LifecycleSubscriber` protocol:

```go
import (
    "github.com/fallguyconsulting/rimsky/protocols/lifecycle"
    genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

type LifecycleServer struct {
    genv1.UnimplementedLifecycleSubscriberServer
}

func (*LifecycleServer) OnTemplateRegistered(ctx context.Context, req *genv1.OnTemplateRegisteredRequest) (*genv1.LifecycleAck, error) {
    return &genv1.LifecycleAck{}, nil  // no-op; or react
}
// ... five more methods (OnTemplateDeployed/Undeployed/Deregistered, OnInstanceCreated/Terminated)
```

Six methods total. Return `nil` from any method you don't react to.

The bundled producers (`stores/{filesystem,postgres,stub}/lifecycle/`) ship a no-op subscriber that returns `nil` from every method. Each producer's `server.go` exposes a `Config.EnableLifecycle bool`; when set, the gRPC server registers `RegisterLifecycleSubscriberServer` and the HTTP bridge mounts the lifecycle routes. Operators opt in via `enable_lifecycle: true` in the producer's own config.

Idempotency is tracked control-api-side in `rimsky_lifecycle_idempotency`; events are keyed by (peer-name, event-type, object-id). Replays are no-ops.

---

## 7. Conformance

Every claim producer **must** pass the conformance suite:

```bash
rimsky-claim-producer-conformance --endpoint my-producer:7000 --transport grpc
```

Test categories (from the service-protocol contract §2.6):

- **Capabilities handshake** — envelope returned, valid values.
- **Open/Commit round-trip** — success path.
- **Open/Abandon round-trip** — failure path.
- **Open/Release round-trip** — release path.
- **Write-semantics uniformity per (producer, scope)** — two `Open` calls returning byte-equal `scope` return the same `realized_write_semantics`.
- **Envelope conformance** — every `realized_write_semantics` returned is in the envelope.
- **Idempotency under retry** — same `claim_id` re-`Open` returns matching state.

A passing run is the acceptance gate for merging a new producer into a Rimsky deployment.

---

## 8. Docker image conventions

Convention, not enforcement:

| aspect | recommendation |
| --- | --- |
| base image | matched to the producer's runtime; multi-stage if you compile |
| exposed ports | `grpc_port` for the protocol, `http_port` for the HTTP+JSON bridge, `admin_port` for items insertion / per-policy admin endpoints, metrics/health |
| env vars | prefix with `STORE_<NAME>_*` for process-local config |
| signals | handle SIGTERM by draining in-flight verbs up to a grace period, then exit |
| logs | structured JSON to stderr |
| health | `/health` on the http_port returning `{status, uptime_seconds, ...}` |

Publish images as `rimsky-store-<name>:<version>`. The reference compose file at `deploy/docker-compose.yml` brings up `store-filesystem`, `store-postgres`, and `store-stub` as service-name-DNS-addressable peers.

---

## 9. Observability protocol (optional)

Per the dashboard/observability spec at `docs/specs/2026-05-02-dashboard-and-observability-design.md` §3, claim producers MAY implement a producer observability protocol for dashboard discovery. The runtime protocol is unchanged.

The service exposes:

- `GetCapabilities` — declares which sub-features the producer supports.
- `GetClaim(claim_id)` — snapshot of one claim's lifecycle state.
- `StreamClaim(claim_id)` — replay-then-live stream.
- `ListClaims(filter)` — paginated browse.
- `GetAdminView` — optional admin-shaped view.

Implementations may decline by returning `Unimplemented` for every RPC and false `supports_*` flags from `GetCapabilities`.

The reference `stores/stub/observability.go` and the postgres / filesystem implementations are working examples.

---

## 10. Inertness & encrypt-before-pass

Producer-supplied bytes (`address`, `payload`, `scope`) are inert in Rimsky (blessed invariant 20). Rimsky reads claim content by named-field path **only** at substitution-leaf extraction (`modeling/attribute/substitution.go::walkPath`); never logs, validates, transforms, or otherwise introspects.

Sensitive fields inside any of these bytes can be encrypted at the producer side before the bytes enter Rimsky (operator practice). Rimsky transports ciphertext as opaque bytes; executors decrypt at point of use. Asymmetric is the recommended default — the executor holds the private key; the producer holds the public key.

Document any crypto your producer performs (which fields, which key material, which algorithm) in your README.

---

## 11. Checklist

Before shipping your producer, verify:

- [ ] All five RPCs implemented (`Open`, `Commit`, `Abandon`, `Release`, `Capabilities`).
- [ ] `claim_id` recorded before any state mutation in `Open` (obligation 2).
- [ ] `Commit` / `Abandon` / `Release` are idempotent in `claim_id` (obligation 3).
- [ ] Producer-side TTL / sweep covers orphan reclamation (obligation 1).
- [ ] Sweep visibility timeout is **strictly greater than `5 × heartbeat_interval`** (Rimsky default `heartbeat_interval_ms: 5000` ⇒ ≥ 25s; reference postgres producer ships `300s`).
- [ ] `scope` bytes are canonical: byte-equal correctly indicates conflict (obligation 5).
- [ ] Byte-equal-scope uniformity invariant: two `Open` calls with byte-equal `scope` return the same `realized_write_semantics`.
- [ ] `realized_write_semantics` is always in the producer-declared envelope.
- [ ] `Capabilities()` returns the full set of values your `Open` may return.
- [ ] `rimsky-claim-producer-conformance` passes all scenarios.
- [ ] Metrics/health endpoint exposed.
- [ ] Docker image published.
- [ ] README documents the spec schema, address shape, and lifecycle/sweep semantics.
- [ ] (Optional) `LifecycleSubscriber` registered when `enable_lifecycle: true`.
- [ ] (Optional) Observability protocol implemented or declined cleanly via `Unimplemented`.

---

## 12. References

- **Service-protocol contract:** `docs/specs/2026-05-04-service-protocol-contract.md` §2 — authoritative wire shape, invariants, conformance categories.
- **Foundation contract:** `docs/specs/2026-05-04-foundation-contract.md` §4 — what Rimsky owns on its side (claim handles, scope conflict, orphan reaping).
- **Glossary:** `docs/glossary.md` — vocabulary.
- **Operator guide:** `docs/operator-guide.md` §8 — YAML config shape and capability handshake.
- **Reference implementations:** `stores/filesystem/`, `stores/postgres/`, `stores/stub/`.
- **Conformance binary:** `cmd/rimsky-claim-producer-conformance/`.
