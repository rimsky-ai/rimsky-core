# Service-Protocol Contract

**Status:** Authoritative until v1, 2026-05-04.
**Scope:** Three Rimsky service protocols — `ClaimProducer`, `Executor`, `LifecycleSubscriber`. Wire shapes, Go interfaces, capability handshakes, conformance requirements.
**Authority:** Single source of truth for the service protocol surface. Supersedes the archived stores-redesign-v3 spec (`docs/history/2026-04-27-stores-redesign-v3-design.md`), the cleanup overlay (`docs/history/2026-04-30-stores-protocol-cleanup-design.md`), and the control-plane-and-store-lifecycle spec's service-protocol content (`docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md`).
**Layer position:** The protocols module (`github.com/fallguy/rimsky/protocols`) carries Go interfaces and protobuf bindings; foundation calls a subset (claim verbs, executor dispatch); modeling calls a subset (lifecycle hooks).

---

## 1. Overview

Three protocols live in `protocols/`:

- **`ClaimProducer`** (§2) — produces claim handles for the lock manager. Foundation calls this at acquisition (`Open`), at executor terminal (`Commit`/`Abandon`/`Release` on non-held claims), and at auto-terminal (`Commit`/`Abandon` on held claims).
- **`Executor`** (§4) — runs nodes. Foundation calls this at dispatch.
- **`LifecycleSubscriber`** (§3) — hooks into control-plane lifecycle events. Modeling (control-api) calls this on template/instance state transitions.

Any binary may implement zero, one, or multiple of these protocols. The `rimsky.yml` schema (modeling-layer contract §10) declares each peer with the protocols it implements via a `protocols:` list. A peer's `Capabilities()` is probed per declared protocol at startup; mismatches fail fast.

The `protocols/` Go module has stdlib + grpc + protobuf only as dependencies. External service authors import `protocols/claimproducer` (or executor / lifecycle) to write a custom service without pulling rimsky's transitive deps.

## 2. ClaimProducer

### 2.1 Purpose & scope

A ClaimProducer is a service that produces claim handles for Rimsky's lock manager and reconciles them at terminal. It owns its own state (filesystem paths, postgres rows, in-memory maps for stub); rimsky owns the bookkeeping rows in the foundation persistence.

### 2.2 Wire surface

Five methods, each defined in `protocols/proto/v1/claim_producer.proto`:

```protobuf
service ClaimProducer {
  rpc Open(OpenRequest) returns (ClaimResult);
  rpc Commit(ClaimVerbRequest) returns (ClaimVerbAck);
  rpc Abandon(ClaimVerbRequest) returns (ClaimVerbAck);
  rpc Release(ClaimVerbRequest) returns (ClaimVerbAck);
  rpc Capabilities(CapabilitiesRequest) returns (CapabilitiesResult);
}

message OpenRequest {
  string claim_id = 1;
  bytes spec = 2;                     // opaque to Rimsky
}

message ClaimResult {
  bytes address = 1;
  bytes payload = 2;
  bytes scope = 3;                    // canonicalized scope bytes
  WriteSemantics realized_write_semantics = 4;
}

message ClaimVerbRequest { string claim_id = 1; }
message ClaimVerbAck {}

message CapabilitiesRequest {}
message CapabilitiesResult {
  repeated WriteSemantics write_semantics_envelope = 1;
}

enum WriteSemantics {
  WRITE_SEMANTICS_UNKNOWN = 0;
  WRITE_SEMANTICS_SYNC = 1;
  WRITE_SEMANTICS_STAGED_ASYNC = 2;
  WRITE_SEMANTICS_BLOCKING_ASYNC = 3;
  WRITE_SEMANTICS_READ_ONLY = 4;
}
```

### 2.3 Go interface

```go
package claimproducer

type ClaimProducer interface {
    Open(ctx context.Context, req OpenRequest) (ClaimResult, error)
    Commit(ctx context.Context, claimID uuid.UUID) error
    Abandon(ctx context.Context, claimID uuid.UUID) error
    Release(ctx context.Context, claimID uuid.UUID) error
    Capabilities(ctx context.Context) (CapabilitiesResult, error)
}
```

### 2.4 Types

```go
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
    Spec    json.RawMessage   // opaque to Rimsky except scope substitution-leaf paths
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

### 2.5 Invariants

- **9b.** *(no internal serialization on lock-shaped predicates)* — ClaimProducer implementations MUST NOT internally serialize on lock-shaped predicates. The reader-lease serialization pattern is forbidden for `staged_async`; honest support requires snapshot delegation or native MVCC pass-through.
- **20.** *(claim content is inert in foundation)* — `address`, `payload`, and `scope` are opaque bytes from foundation's perspective; producers MUST NOT assume Rimsky inspects content.
- **Write-semantics uniformity per (producer, scope-bytes).** Across the lifetime of a producer, two `Open` calls returning byte-equal `scope` MUST return the same `RealizedWriteSemantics`. Producers enforce. Foundation relies on this for the conflict predicate.
- **Envelope conformance.** `RealizedWriteSemantics` returned by `Open` MUST be a member of the `WriteSemanticsEnvelope` returned by `Capabilities`.

### 2.6 Conformance

The `rimsky-claim-producer-conformance` binary verifies all of the above against any binary claiming to implement ClaimProducer. Test categories:

- Capabilities handshake (envelope returned, valid values).
- Open/Commit round-trip (success path).
- Open/Abandon round-trip (failure path).
- Open/Release round-trip (release path).
- Write-semantics uniformity per (producer, scope).
- Envelope conformance (every `RealizedWriteSemantics` returned is in the envelope).
- Idempotency under retry (same `claim_id` re-Open returns matching state).

### 2.7 Removed from this protocol

The six lifecycle hooks (`OnTemplateRegistered/Deployed/Undeployed/Deregistered/OnInstanceCreated/Terminated`) are no longer part of `ClaimProducer`. They live in `LifecycleSubscriber` (§3).

### 2.8 Out of scope

- Store-internal queue semantics (e.g., postgres items-table) are not visible at the protocol level.

## 3. LifecycleSubscriber

### 3.1 Purpose & scope

A service hooks into Rimsky's control-plane lifecycle events. The subscriber pattern lets services react to template/instance state transitions (e.g., bootstrap a schema on `OnTemplateDeployed`).

### 3.2 Wire surface

Six methods, defined in `protocols/proto/v1/lifecycle.proto`:

```protobuf
service LifecycleSubscriber {
  rpc OnTemplateRegistered(OnTemplateRegisteredRequest) returns (LifecycleAck);
  rpc OnTemplateDeployed(OnTemplateDeployedRequest) returns (LifecycleAck);
  rpc OnTemplateUndeployed(OnTemplateUndeployedRequest) returns (LifecycleAck);
  rpc OnTemplateDeregistered(OnTemplateDeregisteredRequest) returns (LifecycleAck);
  rpc OnInstanceCreated(OnInstanceCreatedRequest) returns (LifecycleAck);
  rpc OnInstanceTerminated(OnInstanceTerminatedRequest) returns (LifecycleAck);
}
```

### 3.3 Go interface

```go
package lifecycle

type LifecycleSubscriber interface {
    OnTemplateRegistered(ctx context.Context, req OnTemplateRegisteredRequest) error
    OnTemplateDeployed(ctx context.Context, req OnTemplateDeployedRequest) error
    OnTemplateUndeployed(ctx context.Context, req OnTemplateUndeployedRequest) error
    OnTemplateDeregistered(ctx context.Context, req OnTemplateDeregisteredRequest) error
    OnInstanceCreated(ctx context.Context, req OnInstanceCreatedRequest) error
    OnInstanceTerminated(ctx context.Context, req OnInstanceTerminatedRequest) error
}
```

### 3.4 Implementation pattern

Return `nil` from any method the binary doesn't react to. Binaries that don't react to any event simply don't implement the service.

### 3.5 Idempotency

Control-api tracks idempotency in `rimsky_lifecycle_idempotency` (renamed from `rimsky_store_lifecycle`). Each event keyed by (peer-name, event-type, object-id). Replays are no-ops.

### 3.6 Conformance

`rimsky-conformance --check-lifecycle` mode (combined with executor conformance into one binary).

### 3.7 Out of scope

- Bidirectional events from peer back to Rimsky (peer can't initiate).
- Cross-peer event ordering guarantees.

## 4. Executor

### 4.1 Purpose & scope

An Executor runs nodes given inputs. Foundation calls into the executor at dispatch.

### 4.2 Wire surface

Defined in `protocols/proto/v1/executor.proto`:

```protobuf
service Executor {
  rpc Execute(ExecuteRequest) returns (ExecuteResponse);
  rpc StreamTrace(TraceRequest) returns (stream TraceEvent);
  rpc GetTrace(TraceRequest) returns (TraceBundle);
  rpc GetCapabilities(CapabilitiesRequest) returns (ExecutorCapabilities);
}
```

### 4.3 Go interface

```go
package executor

type Executor interface {
    Execute(ctx context.Context, req ExecuteRequest) (ExecuteResponse, error)
    StreamTrace(ctx context.Context, req TraceRequest, send func(TraceEvent) error) error
    GetTrace(ctx context.Context, req TraceRequest) (TraceBundle, error)
    GetCapabilities(ctx context.Context) (ExecutorCapabilities, error)
}
```

### 4.4 Async-callback path

For async terminals, the executor POSTs to `${callback_url}/v1/callback/{async_ack_id}` with body keyed `type` (not `kind`). The supervisor's chi route binds this exact shape.

Sync-terminal sequence:

1. Foundation: `Execute(req)`.
2. Executor: runs work; returns `ExecuteResponse{terminal: ...}`.

Async-terminal sequence:

1. Foundation: `Execute(req)`.
2. Executor: returns `ExecuteResponse{async_ack_id: ...}` immediately.
3. Executor (later): `POST ${callback_url}/v1/callback/{async_ack_id}` body `{"type": "success", ...}` or `{"type": "failure", ...}`.
4. Foundation: receives callback; processes terminal.

### 4.5 Capabilities response

`ExecutorCapabilities` includes `http_bridge_url` for dashboard discoverability. Allows the dashboard to render an HTTP+JSON UI surface against the executor.

### 4.6 Userdata-is-opaque

Modeling-layer invariant 11 re-asserted: executors MUST receive `userdata` verbatim (no substitution applied by Rimsky); rimsky doesn't introspect.

### 4.7 Conformance

`rimsky-conformance --check-executor` mode. Test categories:

- Capabilities handshake.
- Execute sync terminal (success).
- Execute sync terminal (failure).
- Execute async terminal (success).
- Execute async terminal (failure).
- Trace streaming.
- Trace retention (parameterized by `--retention-test-seconds`).

### 4.8 Out of scope

- Observability protocol (separate spec).
- Execution semantics (executor-internal).

## 5. Capability handshake protocol

At startup, control-api / supervisor / scheduler:

1. Read the `claim_producers:` and `executors:` blocks from `rimsky.yml`.
2. For each peer, dial the endpoint.
3. For each protocol the peer claims to implement (per its `protocols:` list), call `Capabilities()`.
4. Validate operator-declared properties against producer-declared envelope (e.g., operator's `write_semantics_envelope: [staged_async]` ⊆ producer's `WriteSemanticsEnvelope: [staged_async, sync]`).
5. Fail fast on any mismatch.

The handshake is one-shot at startup. Capabilities are cached for the process's lifetime; Capabilities changes require restart.

## 6. Conformance binaries

Three binaries:

- **`rimsky-conformance`** — covers Executor and LifecycleSubscriber. Flags: `--endpoint`, `--transport grpc|http+json`, `--check-executor`, `--check-lifecycle`, `--retention-test-seconds`, `--require-stub-mode`.
- **`rimsky-claim-producer-conformance`** — renamed from `rimsky-store-conformance`. Covers ClaimProducer.
- **`rimsky-conformance-probe`** — utility helper, retained as-is. Used by `rimsky-conformance --require-stub-mode` to verify executor stub-mode at startup.

## 7. Migration & vocabulary

This contract:

- **Renames** `Store` → `ClaimProducer` at the protocol level. The "store" colloquialism survives at the bundled-services layer for data-backed ones (filesystem store, postgres store, stub store).
- **Renames** `region` → `scope` everywhere on the wire. Proto field `bytes region` → `bytes scope`; `RegionConflict` message → `ScopeConflict`.
- **Splits** lifecycle hooks out of `Store` into the new `LifecycleSubscriber` service.
- **Adds** `RealizedWriteSemantics` to `ClaimResult`. Producers must return this per claim; foundation stores it on the claim handle.
- **Adds** `WriteSemanticsEnvelope` to `CapabilitiesResult`, replacing the single `WriteSemantics` field. Operators declare a subset envelope per peer in YAML.

## 8. Out of scope

- Dashboard observability protocols (separate spec).
- Bundled-service implementations (covered in `docs/claim-producer-author-guide.md` and `docs/executor-author-guide.md`).
- YAML config shape (covered in modeling contract §10).

---

*End of contract.*
