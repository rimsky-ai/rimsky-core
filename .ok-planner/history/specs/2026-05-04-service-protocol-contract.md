# Service-Protocol Contract

**Status:** Authoritative until v1, 2026-05-04.
**Scope:** Three Rimsky service protocols — `ClaimProducer`, `Executor`, `LifecycleSubscriber`. Wire shapes, Go interfaces, capability handshakes, conformance requirements.
**Authority:** Single source of truth for the service protocol surface. Supersedes the archived stores-redesign-v3 spec (`docs/history/2026-04-27-stores-redesign-v3-design.md`), the cleanup overlay (`docs/history/2026-04-30-stores-protocol-cleanup-design.md`), and the control-plane-and-store-lifecycle spec's service-protocol content (`docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md`).
**Layer position:** The protocols module (`github.com/fallguyconsulting/rimsky/protocols`) carries Go interfaces and protobuf bindings; foundation calls a subset (claim verbs, executor dispatch); modeling calls a subset (lifecycle hooks).

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
  rpc Capabilities(CapabilitiesRequest) returns (CapabilitiesResponse);
  rpc Open(OpenRequest) returns (OpenResponse);
  rpc Commit(CommitRequest) returns (CommitResponse);
  rpc Abandon(AbandonRequest) returns (AbandonResponse);
  rpc Release(ReleaseRequest) returns (ReleaseResponse);
}

message CapabilitiesRequest {}
message CapabilitiesResponse {
  repeated WriteSemantics write_semantics_envelope = 1;
}

// OpenRequest carries the rimsky-generated claim_id plus the resolved
// claim spec. selector is post-substitution; the producer parses it.
// template_id and instance_id form the per-spec scope envelope: opaque
// to rimsky, available to the producer for namespace routing.
message OpenRequest {
  string claim_id = 1;
  string store_name = 2;
  string selector = 3;
  string intent = 4;          // "r" | "rw"
  string alias = 5;
  string template_id = 6;     // content hash; opaque to rimsky.
  string instance_id = 7;     // instance UUID; opaque to rimsky.
}

// OpenResponse signals acquisition outcome via a oneof. Producers that
// always have a claim to give return Acquired. Producers that may have
// nothing right now (e.g. an empty items-table queue) return Unavailable.
message OpenResponse {
  oneof result {
    Acquired    acquired    = 1;
    Unavailable unavailable = 2;
  }
}

message Acquired {
  bytes address = 1;
  bytes payload = 2;
  bytes scope   = 3;          // canonicalized scope bytes
  WriteSemantics realized_write_semantics = 4;
}

message Unavailable {}

message CommitRequest  { string claim_id = 1; bytes scope = 2; bytes address = 3; }
message CommitResponse {}
message AbandonRequest { string claim_id = 1; bytes scope = 2; bytes address = 3; }
message AbandonResponse {}
message ReleaseRequest { string claim_id = 1; bytes scope = 2; bytes address = 3; }
message ReleaseResponse {}

enum WriteSemantics {
  WRITE_SEMANTICS_UNKNOWN = 0;
  WRITE_SEMANTICS_SYNC = 1;
  WRITE_SEMANTICS_STAGED_ASYNC = 2;
  WRITE_SEMANTICS_BLOCKING_ASYNC = 3;
  WRITE_SEMANTICS_READ_ONLY = 4;
}
```

### 2.3 Go interface

The rimsky-side and external-author Go surface mirror the wire
shape: structured `ClaimSpec`, `OpenOutcome` discriminator for the
oneof, and per-verb `(claimID, scope, address)` for the terminal
verbs. The canonical Go interface lives at
`protocols/claimproducer.ClaimProducer`. `foundation/locks.ClaimProducer`
is a Go type alias of that interface (`type ClaimProducer =
claimproducer.ClaimProducer`); the same applies to every supporting
type (`ClaimID`, `ClaimSpec`, `ClaimResult`, `OpenOutcome`,
`Capabilities`, `WriteSemantics`, `Intent`). External authors should
import `protocols/claimproducer`; rimsky-internal code may use either.
The two import paths refer to the same nominal types — a value
satisfying one interface satisfies the other.

```go
package claimproducer

type ClaimProducer interface {
    Name() string
    Capabilities(ctx context.Context) (Capabilities, error)
    Open(ctx context.Context, claimID ClaimID, spec ClaimSpec) (OpenOutcome, error)
    Commit(ctx context.Context, claimID ClaimID, scope []byte, address []byte) error
    Abandon(ctx context.Context, claimID ClaimID, scope []byte, address []byte) error
    Release(ctx context.Context, claimID ClaimID, scope []byte, address []byte) error
}
```

### 2.4 Types

```go
type ClaimID string
type Intent string
const (
    IntentRead      Intent = "r"
    IntentReadWrite Intent = "rw"
)

type ClaimSpec struct {
    StoreName  string
    Selector   string
    Intent     Intent
    Alias      string
    TemplateID string
    InstanceID string
}

type ClaimResult struct {
    Address                json.RawMessage
    Payload                json.RawMessage
    Scope                  json.RawMessage   // canonicalized scope bytes
    RealizedWriteSemantics WriteSemantics    // per-claim
}

// OpenOutcome mirrors the OpenResponse oneof on the wire.
type OpenOutcome struct {
    Available bool        // false → Unavailable{}
    Result    ClaimResult // populated only when Available is true
}

type WriteSemantics string
const (
    WriteSemanticsUnknown       WriteSemantics = ""
    WriteSemanticsSync          WriteSemantics = "sync"
    WriteSemanticsStagedAsync   WriteSemantics = "staged_async"
    WriteSemanticsBlockingAsync WriteSemantics = "blocking_async"
    WriteSemanticsReadOnly      WriteSemantics = "read_only"
)

type Capabilities struct {
    WriteSemanticsEnvelope []WriteSemantics
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
    Name() string // operator-configured peer name (rimsky-internal)

    OnTemplateRegistered(ctx context.Context, req OnTemplateRegisteredRequest) error
    OnTemplateDeployed(ctx context.Context, req OnTemplateDeployedRequest) error
    OnTemplateUndeployed(ctx context.Context, req OnTemplateUndeployedRequest) error
    OnTemplateDeregistered(ctx context.Context, req OnTemplateDeregisteredRequest) error
    OnInstanceCreated(ctx context.Context, req OnInstanceCreatedRequest) error
    OnInstanceTerminated(ctx context.Context, req OnInstanceTerminatedRequest) error
}

type OnTemplateRegisteredRequest struct {
    TemplateHash string
    Spec         json.RawMessage // canonical JCS-canonicalized bytes
}
type OnTemplateDeployedRequest struct {
    TemplateHash string
    Tags         []string
}
type OnTemplateUndeployedRequest    struct { TemplateHash string }
type OnTemplateDeregisteredRequest  struct { TemplateHash string }
type OnInstanceCreatedRequest struct {
    InstanceID   string
    TemplateHash string
    InstanceKey  string          // may be empty
    Params       json.RawMessage
}
type OnInstanceTerminatedRequest struct {
    InstanceID         string
    TemplateHash       string
    TerminatedAtUnixMs int64
}
```

Subscribers receive the full per-event payload that the wire-side
proto messages carry. The canonical Go interface and request types
live in `protocols/lifecycle`. `foundation/locks.LifecycleSubscriber`
and the `On…Request` types in `foundation/locks` are Go type aliases
of the corresponding `protocols/lifecycle` symbols, so external
authors importing `protocols/lifecycle` and rimsky-internal callers
importing `foundation/locks` share one nominal type. The interface
includes `Name() string` (operator-configured peer name; rimsky-side
identifier; not transported over the wire) plus the six event
methods.

### 3.4 Implementation pattern

Return `nil` from any method the binary doesn't react to. Binaries that don't react to any event simply don't implement the service.

### 3.5 Idempotency

Control-api tracks idempotency in `rimsky_lifecycle_idempotency` (renamed from `rimsky_store_lifecycle`). Each event keyed by (peer-name, event-type, object-id). Replays are no-ops.

### 3.6 Conformance

`rimsky-executor-conformance --check-lifecycle` mode (combined with executor conformance into one binary).

### 3.7 Out of scope

- Bidirectional events from peer back to Rimsky (peer can't initiate).
- Cross-peer event ordering guarantees.

## 4. Executor

### 4.1 Purpose & scope

An Executor runs nodes given inputs. Foundation calls into the executor at dispatch.

### 4.2 Wire surface

Defined in `protocols/proto/v1/executor.proto` (the service is named
`NodeExecutor` for backwards compatibility with the v2-era client
helpers; the protocol is identical):

```protobuf
service NodeExecutor {
  // Execute returns a stream of zero or more Heartbeat events
  // followed by EXACTLY ONE terminal event (Complete | Blocked |
  // Errored | AsyncAccepted).
  rpc Execute(ExecuteRequest) returns (stream ExecuteEvent);
}
```

Trace endpoints (StreamTrace / GetTrace / GetCapabilities) live in
the executor-observability protocol — see the dashboard /
observability spec — and are not part of the runtime executor wire
surface.

### 4.3 Go interface

The Go-level interface mirrors the streaming wire shape: `Execute`
returns a transport-agnostic `Stream` that the supervisor drains.

```go
package executor

type Executor interface {
    Execute(ctx context.Context, req ExecuteRequest) (Stream, error)
}

type Stream interface {
    Recv() (ExecuteEvent, error) // io.EOF after the terminal event
    Close() error
}

// ExecuteEvent is the union of streamed events; exactly one terminal
// closes the stream.
type ExecuteEvent struct {
    Heartbeat     *Heartbeat
    Complete      *Complete
    Blocked       *Blocked
    Errored       *Errored
    AsyncAccepted *AsyncAccepted
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

`rimsky-executor-conformance --check-executor` mode. Test categories:

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

- **`rimsky-executor-conformance`** — covers Executor and LifecycleSubscriber. Flags: `--endpoint`, `--transport grpc|http+json`, `--check-executor`, `--check-lifecycle`, `--retention-test-seconds`, `--require-stub-mode`.
- **`rimsky-claim-producer-conformance`** — renamed from `rimsky-store-conformance`. Covers ClaimProducer.
- **`rimsky-conformance-probe`** — utility helper, retained as-is. Used by `rimsky-executor-conformance --require-stub-mode` to verify executor stub-mode at startup.

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
