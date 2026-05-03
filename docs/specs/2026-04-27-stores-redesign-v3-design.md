# Stores Redesign v3 — Out-of-Process Stores

**Date:** 2026-04-27
**Predecessor:** `docs/specs/2026-04-27-stores-redesign-v2-design.md` (v2; landed as commit `e46b952`).
**Genesis:** `docs/history/2026-04-26-stores-redesign.md` (the original out-of-process design discussion; this spec implements §17 #3 and the L1 row from the v2 spec-scope working doc) and `docs/history/2026-04-27-store-protocol-inertness-cleanup.md` (the cleanup notes folded into v3 because they are natural consequences of OOP).
**Glossary:** `docs/glossary.md` is authoritative for vocabulary.

---

## 1. Background

The v2 spec designed the protocol surface — five verbs (`Open` / `Commit` / `Abandon` / `Delete` / `Release`), opaque selectors, two primitives (claim, named lock), single capability field (`write_semantics`), inertness invariant 20, frame interaction, claim/lock unification — and explicitly deferred the out-of-process implementation (row L1 of the spec-scope working doc) to a separate cycle. That cycle is this spec.

The v2 implementation kept the standard store impls (`filesystem`, `postgres`, `stub`) as in-process Go subpackages of `core/store/`, with the supervisor's atomic acquisition transaction sharing a `pgx.Tx` with the in-process store via `store.WithTx` / `store.TxFromContext`. This satisfied invariant 10 (lock acquisition atomic with dispatch claim) by literal tx-sharing — possible only because both sides ran on the same Postgres connection.

Post-v2 review surfaced several places where rimsky reaches *inside* store state via non-protocol surfaces — type assertions to `*pgstore.Store`, store-only methods (`InsertItems`, `PickPolicyConfig`, `PickPolicies`), a validator hook that classifies pick-policy selectors, a scheduler sweep that runs SQL against operator-owned items tables. These violations are tolerable in the in-process world (the store is colocated) but block OOP entirely: every rimsky↔store interaction must be expressible over the 5-verb protocol with no in-process type assertions.

v3 moves the standard store impls out-of-process and strips the store-knowledge violations as a natural consequence. The protocol designed in v2 is portable to OOP without redesign; v3 implements the wire form, the atomicity-without-tx-sharing redesign, the operator-config rewrite, and the documentation pass.

## 2. Goals and non-goals

### 2.1 Goals

- Move the standard store implementations (`filesystem`, `postgres`, `stub`) out of `core/store/` into standalone binaries under a new `stores/` directory, mirroring `executors/`.
- Define the wire protocol for the 5 runtime verbs plus a `Capabilities()` startup-handshake RPC.
- Strip rimsky-side store construction: no `Factory` interface, no `Registry.BuildAll`, no `StoresConfig` schema in rimsky, no per-kind dispatch.
- Strip rimsky-side store introspection: the four inertness violations (admin endpoint, validator hook, scheduler visibility-timeout sweep, store-only methods on the in-process postgres store) all dissolve.
- Redesign invariant 10 for the no-tx-sharing world: store owns its own transaction; rimsky owns its bookkeeping transaction; the two are decoupled.
- Add a `claim_id` (rimsky-generated UUID) parameter to every protocol verb, present even when the store doesn't need it.
- Rewrite the operator config schema to a thin "store name → endpoint + declared capabilities" form.
- Rewrite the smoke fixture and the docker-compose deployment for the multi-binary topology.
- Slim the auth-blind documentation across `store-author-guide.md`, `executor-author-guide.md`, `operator-guide.md` to brief advisory framing only.

### 2.2 Non-goals

- Multi-tenant store provisioning (control-layer concern; spec at `docs/2026-04-26-control-layer.md`).
- Control-layer auth.
- Encrypt-before-pass machinery: rimsky has no encryption mechanism; documentation reduces to a brief advisory ("rimsky doesn't keep your secrets; encrypt sensitive data before passing it through").
- Bridge framework or polyglot SDKs for store-service authors. Rimsky specifies the protocol; implementers handle SDKs.
- A `staged_async` standard store. Protocol supports it (`Open(read intent)` + `Release` at terminal); no v3 standard store exercises it.
- `core/queue/DispatchQueue` pgx leak (still present; addressed when shipping a non-postgres platform state backend, which is a separate cycle).
- Schema changes to rimsky's bookkeeping tables (the only adjustment is moving `rimsky_lock_holders.id` from server-side `gen_random_uuid()` default to client-side generation; the column shape stays).

## 3. Principles

Three rules. Every design choice in this spec follows from them.

### 3.1 Protocol-only

Rimsky talks to stores exclusively through the 5 protocol verbs (`Open`, `Commit`, `Abandon`, `Delete`, `Release`) plus the `Capabilities()` startup-handshake RPC. Type assertions to a concrete store type in any rimsky package are forbidden. The `Store` interface is the only contract.

### 3.2 Opaque selectors

Selectors are strings rimsky carries unchanged from template DSL (`{{...}}`-substituted at dispatch) through to store verbs. Rimsky does not parse, classify, or look up selectors against store state. Pick-policy selectors (the `@policy-name` convention) are a store-side recognition; rimsky has no pick-policy concept.

### 3.3 Store-internal capabilities

Queue maintenance, items tables, pick policies, visibility-timeout sweeps, item seeding, staging cleanup — all store-internal. A store-service that wants to expose item-management endpoints does so on its own service surface, not by lending its concrete type to rimsky.

The implication for v3: the four inertness violations from v2 (admin items endpoint, store-only methods on `*pgstore.Store`, the pick-policy validator hook, the scheduler visibility-timeout sweep) all dissolve. They are not "cleanup" — they are structurally impossible once stores are out-of-process.

## 4. Protocol surface

### 4.1 The runtime verbs (5 + 1)

Five claim-lifecycle verbs:

```
Open(claim_id, ClaimSpec) → ClaimResult
Commit(claim_id, region, address, policy_override?) → ()
Abandon(claim_id, region, address?, policy_override?) → ()
Delete(claim_id, region) → ()
Release(claim_id, region, address) → ()
```

One startup-handshake verb:

```
Capabilities() → CapabilityStruct
```

`Capabilities()` is not a runtime verb — it is called once per store-service per rimsky process at startup, the result is cached, and the result is validated against the operator's declared requirements (§6). Mismatch fails the rimsky process at startup.

### 4.2 `claim_id`

`claim_id` is a UUID generated by rimsky immediately before `Open`. Rimsky persists it in its own bookkeeping (`rimsky_lock_holders.id`) and passes it on every subsequent verb call for that claim's lifecycle. Rimsky guarantees:

- Same `claim_id` flows through every call for a given claim.
- `claim_id` is generated client-side so it is known *before* any RPC is made.

The store is non-prescriptive about `claim_id` use. It may key its internal state by `claim_id`, by `address`, by both, or by neither. Stores that don't need a key can ignore it; stores that need a stable correlation across verbs use it.

**Lifecycle on Open failure.** If `Open` fails (RPC error, store-side error, timeout), the supervisor rolls back its rimsky-side tx — the `rimsky_lock_holders` row is never persisted, the `claim_id` is discarded. The supervisor does NOT call `Abandon` after an Open failure (there is no persisted claim to clean up from rimsky's side). Any partial store state that resulted from the failed Open is the store's responsibility to clean up via its TTL/sweep, identifying the orphan by `claim_id` (which the store may have recorded internally before the RPC response was lost). This is consistent with §7.5's default "no rimsky-driven Abandon."

### 4.3 `address` semantics

`address` is opaque bytes the store-service returns from `Open`. Rimsky stores it (in `rimsky_lock_holders.address`) and passes it back on `Commit` / `Abandon` / `Release`. Executors receive it in their dispatch envelope and use it to talk to the underlying storage (postgres database, file system, S3 bucket, etc.) directly on the data path — the store-service is not in the data path.

Address may be empty in two cases:
- The store genuinely has nothing to return (e.g., a regional `r` claim on `direct` write-semantics — no staging, no snapshot, address is empty bytes).
- `Open` failed before the store could return one (RPC error, timeout, connection drop). In this case `Abandon` may be called with a nil/empty address; the store uses `claim_id` to identify whether it has any state to clean up.

### 4.4 `region`

`region` is the resolved selector text — the bytes the store uses to identify what's being claimed. For pick-policy claims this may be the store's own identifier for the picked item (returned in `ClaimResult.Region`); for static-selector claims it's the selector text the supervisor sent to `Open`. Rimsky persists it on the lock-holder row (`region_data` column) and passes it back on every subsequent verb.

### 4.5 `policy_override`

Optional argument on `Commit` / `Abandon`. Store-internal vocabulary for stores that implement pick policies (`release_to_back`, `release_to_head`, `delete`, etc.). Stores that don't run pick policies ignore the field. Rimsky reads the value from the template's `claim_resolutions:` block on the acquirer node and passes it through; rimsky does not enumerate or validate the values.

### 4.6 `ClaimSpec`

```
ClaimSpec {
  store_name: string
  selector:   string         // resolved at dispatch; store's grammar
  intent:     "r" | "rw"
  alias:      string         // template-side identifier; store ignores
}
```

Carried unchanged from v2. `alias` is for substitution-path resolution on the rimsky side and is opaque to the store.

### 4.7 `ClaimResult`

```
ClaimResult {
  address: bytes (json.RawMessage)   // store-native pointer for executor data path
  payload: bytes (json.RawMessage)   // store-emitted payload (e.g., picked item content)
  region:  bytes (json.RawMessage)   // store's identifier for what was claimed
}
```

All three fields are opaque bytes per invariant 20. Rimsky reads them only via `walkPath` for substitution-leaf extraction. The store may populate any or all; rimsky persists what it gets.

**Pool-empty signal.** A `ClaimResult` with all three fields empty (zero-length bytes for address, payload, AND region) is the store's signal to rimsky that "no item is available for this pick-policy claim." This is the v2 convention preserved unchanged. The supervisor treats this as a successful RPC outcome that produces no dispatch — it does NOT proceed with the dispatch and does NOT mark the claim as held; the claim attempt is silently skipped and may retry on a future tick. Carrying this convention over the wire requires no protocol change beyond the existing field semantics.

### 4.8 `Capabilities()` and the capability struct

```
CapabilityStruct {
  write_semantics: "direct" | "staged_blocking" | "staged_async"
}
```

Single field for v3 (forward-compat for additional fields). Rimsky calls `Capabilities()` once per store-service at startup and validates strict equality against the operator's declared requirements. Mismatch → startup error with a clear message naming the store, the declared capabilities, and the actual capabilities.

**Supersedes v2 §8.1's downgrade semantic.** v2 allowed the operator to downgrade the store's max capability (e.g., force `direct` on a store that supported `staged_blocking`). v3 changes this: each store-service runs at exactly one `write_semantics` (the value baked into its own config per §6.3); rimsky's job is to verify the operator's declared expectation matches that value, not to negotiate a downgrade. If an operator wants a different `write_semantics` for the same store, they run two separate store-services with different configs, registered under different names. The shape is more uniform; the store-side config is the source of truth.

### 4.9 v2 spec sections superseded

This v3 spec supersedes the following v2 sections. None should be retroactively edited (per the principle established in `docs/history/2026-04-27-store-protocol-inertness-cleanup.md`); they remain in the v2 spec as historical record. The supersession is documented here so future readers see the trail.

- **v2 §8.1 (operator downgrade of store max).** Superseded by v3 §4.8 / §6.2: strict equality between operator-declared and store-advertised capabilities. Operators run two store-services for two semantics rather than downgrading at config-load time. (Already documented in §4.8.)
- **v2 §8.2 (`Factory.MaxWriteSemantics()` + `BuildAll`-time validation).** Superseded by v3 §6.3 (per-store-service config baked in at store-service startup; `Factory` and `BuildAll` removed entirely per §11.1).
- **v2 §11.6 (transaction-context helpers `WithTx` / `TxFromContext`).** Superseded by v3 §7.3 (decoupled tx model; `core/store/tx.go` removed entirely per §11.1).
- **v2 §13.3 (atomic acquisition flow with `Store.Open` invoked under the shared `pgx.Tx`).** Superseded by v3 §7.3. The acquisition flow now opens a rimsky-side tx, RPCs `Open` over the wire, and the store runs its state mutation in its own independent tx.
- **v2 §13.5 step 1 (`Store.Abandon` invoked from the orphan reap path).** Superseded by v3 §7.5 (default: orphan reaper deletes the lock-holder row without RPCing `Abandon`; store's TTL handles cleanup) and §11.2's `reapRegionRow` modification.

### 4.10 Blessed-invariant retirement and revision

Two blessed invariants from v2 require action because their underlying mechanism changes or disappears.

**Invariant 14 — RETIRED.**

> v2 wording: "**`RegionsConflict` and `UnmarshalRegion` are pure.** No side effects, no external state read; deterministic on inputs."

The two methods are removed in v3 (§11.1, §7.7). The invariant is vacuous under v3. Action: delete invariant 14 from `CLAUDE.md`'s blessed-invariant list and from any `@blessed-invariant 14` annotations in source code.

**Invariant 15 — REVISED.**

> v2 wording: "**`Open` fires inside the acquisition transaction (in-process).** The supervisor's atomic acquisition transaction calls `Store.Open` with the open `pgx.Tx` shared via `store.TxFromContext`. Store-side state mutations and the lock-holder row's `address` update participate in the same transaction."

In v3, `Open` still fires inside the rimsky-side acquisition tx (§7.3 step 4) — so the invariant's spirit is preserved — but the store's state mutation is no longer in the same transaction (the store is over the wire and runs its own tx). The "(in-process)" parenthetical and the "shared via `store.TxFromContext`" clause are no longer accurate.

Revised wording for v3:

> "**`Open` fires inside the rimsky-side acquisition transaction.** The supervisor's atomic acquisition transaction calls `Store.Open` between the lock-holder row INSERT and the rimsky-side COMMIT. The store's own state mutation runs in its own transaction (store-internal, decoupled from rimsky's). The atomicity guarantee on the rimsky side (dispatch claim + lock-holder INSERT + address UPDATE) holds; store atomicity is the store's concern."

Action: update `CLAUDE.md`'s invariant-15 entry; update any `@blessed-invariant 15` annotations in source code (e.g., `core/supervisor/runner_acquire.go`).

**Invariant 10 — CLARIFIED.**

> v2 wording: "**Lock acquisition is atomic with dispatch claim.** The §13.3 step-3 transaction either claims dispatch AND inserts all required `rimsky_lock_holders` rows AND completes all `Store.Open` mutations, or none of these."

In v3, the literal "completes all `Store.Open` mutations" clause no longer holds — the store's mutations now run in its own tx, decoupled from rimsky's per §7.3. The semantic guarantee for **rimsky-side state** is preserved: dispatch claim + lock-holder INSERT + address UPDATE are still all-or-nothing. The store's state is no longer in the same atomic envelope; store atomicity is the store's concern (§7.8 obligation #1).

Revised wording for v3:

> "**Lock acquisition is atomic with dispatch claim (rimsky-side).** The §7.3 atomic acquisition transaction either claims dispatch AND inserts all required `rimsky_lock_holders` rows AND records the `Store.Open`-returned address, or none of these. The store's own state mutations run in a store-internal transaction decoupled from rimsky's; store atomicity is the store's concern (§7.8). Single-writer-per-region (invariant 4b) holds because the rimsky-side conflict predicate gates all lock-holder INSERTs against `rimsky_lock_holders` only — store orphan state is invisible to the predicate."

Action: update `CLAUDE.md`'s invariant-10 entry; update any `@blessed-invariant 10` annotations in source code.

The other v2 blessed invariants carry forward unchanged. Invariant 4 (claimant-guarded release) and invariant 4b (single-writer-per-region) are distinct: 4 is the claimant-guard on every DELETE / claim-clearing UPDATE; 4b is the structural single-writer-per-region rule preserved by rimsky-side bookkeeping per §7.6. (The 4 / 4b split clarifies wording that pre-v3 sometimes used "invariant 4" for both senses.)

## 5. Wire format

Two encodings of the same protocol — gRPC primary, HTTP+JSON bridge for clients without proto tooling. Mirrors the executor protocol.

### 5.1 gRPC

A single `proto/v1/store_service.proto` defines the service:

```proto
service StoreService {
  rpc Capabilities(CapabilitiesRequest) returns (CapabilitiesResponse);
  rpc Open(OpenRequest)                 returns (OpenResponse);
  rpc Commit(CommitRequest)             returns (CommitResponse);
  rpc Abandon(AbandonRequest)           returns (AbandonResponse);
  rpc Delete(DeleteRequest)             returns (DeleteResponse);
  rpc Release(ReleaseRequest)           returns (ReleaseResponse);
}
```

Generated bindings live under `proto/v1/gen/`. Each standard store-service implements the gRPC server; rimsky's `core/store/remote/` implements the gRPC client.

### 5.2 HTTP+JSON bridge

Each store-service exposes a parallel HTTP+JSON surface mounted on the same process at a configurable port (store-author choice; default e.g. `:9100` gRPC, `:9101` HTTP). Routes follow the executor pattern:

```
POST /v1/capabilities
POST /v1/open
POST /v1/commit
POST /v1/abandon
POST /v1/delete
POST /v1/release
```

Request and response bodies are JSON encodings of the proto messages (using protobuf's canonical JSON mapping). The HTTP bridge is hand-written route handlers per store-service binary that decode JSON, marshal to the proto type, call the same internal handler the gRPC server calls. Same shape as the executors' HTTP bridge.

### 5.3 Errors

Errors return as gRPC status codes (or HTTP status codes on the bridge) plus a structured error message. The store distinguishes:

- **Store-side commit error** (store-honest "I cannot do this"): merge conflict, conditional-put failure, serializability violation, quota exceeded, items-table empty for a pick-policy `Open`. Routes through rimsky's existing `retry / give_up / invalidate(targets)` vocabulary at the supervisor's terminal handler.
- **Transport error** (network drop, store-service down): rimsky retries the verb at the supervisor level; `Open` retries surface as new claim attempts, `Commit` retries are at-least-once-delivery (store must be idempotent in `claim_id` to handle this).
- **Protocol error** (malformed request, unknown verb): rimsky logs and surfaces as an error to the operator; the dispatch fails as `protocol_error`.

## 6. Operator config

### 6.1 Schema

`stores.yml` collapses to a thin name → endpoint + declared-requirements form:

```yaml
stores:
  content:
    endpoint: "grpc://store-filesystem:9100"
    capabilities:
      write_semantics: direct
  topics:
    endpoint: "grpc://store-postgres:9101"
    capabilities:
      write_semantics: direct

named_locks:
  model-calls:        { limit: 5 }
  pipeline-singleton: { limit: 1 }
```

The `capabilities:` block declares what the operator *requires* the store-service to advertise. The `named_locks:` block is unchanged from v2.

The schema explicitly does NOT contain:

- `kind` — there is no per-kind dispatch in rimsky; every store is a remote gRPC client.
- `connection` — store connection details live in the store-service's own config, not in rimsky's view.
- `pick_policies` — store-internal; the store-service has its own config for pick policies.
- Any other store-specific keys.

### 6.2 Discovery

Rimsky processes (`rimsky-supervisor`, `rimsky-scheduler`, `rimsky-control-api`) load `stores.yml` from `RIMSKY_STORES_CONFIG` at startup. For each entry:

1. Parse `endpoint` URL; build a gRPC client.
2. Call `Capabilities()` over the wire.
3. Validate the returned capability struct strictly equals the declared `capabilities:` block.
4. On any failure (unreachable, protocol error, capability mismatch), the rimsky process exits with a clear error message naming the store and the failure mode.
5. On success, register the store under its operator-chosen name in the rimsky process's `core/store/Registry` (now a simple `map[string]Store`).

After successful startup, rimsky runs as long as the store-services are reachable. Mid-runtime store-service unreachability surfaces as RPC errors on the affected verbs (per §5.3).

### 6.3 Per-store-service config

Each store-service binary owns its own config schema and loads it from its own env var (e.g., `STORE_POSTGRES_CONFIG=/etc/store-postgres/config.yml`). The schema is store-specific and out of rimsky's view. For the standard stores:

- `stores/filesystem/`: `root` (filesystem path), `write_semantics`, gRPC + HTTP listen ports.
- `stores/postgres/`: `connection` (Postgres DSN), `write_semantics`, `pick_policies` (store-internal map; queue/ring/etc. configurations), gRPC + HTTP listen ports.
- `stores/stub/`: in-memory state; minimal config; gRPC + HTTP listen ports.

Each store-author guide documents its own config schema. Rimsky neither defines nor validates these schemas.

## 7. Architecture

### 7.1 Process topology

The reference deployment runs as separate processes:

- `rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api` (rimsky's own processes; unchanged from v2 in shape).
- `store-filesystem`, `store-postgres`, `store-stub` (one binary per standard store; new in v3).
- `executor-http-node`, `executor-claude-agent` (executors; unchanged from v2).
- `postgres` (rimsky's own state backend).

The store-services may run on the same host as rimsky (collocated), on separate hosts, or in separate clusters. Rimsky has no opinion; configuration is purely the endpoint URL.

### 7.2 Data path

Unchanged from v2: executor receives `address` from rimsky in the dispatch envelope, talks to the underlying storage directly using its native protocols (file I/O, SQL, S3 SDK, etc.). The store-service is on the control plane only — it does not proxy data.

### 7.3 Atomicity model

Today's atomic acquisition transaction (v2) does, all in one `pgx.Tx`:

1. Claims dispatch row.
2. Inserts `rimsky_lock_holders` rows.
3. Calls `Store.Open` in-process (store uses the open tx via `TxFromContext`).
4. Updates `rimsky_lock_holders.address` with returned address.
5. Inserts `rimsky_claim_holders` rows.
6. Commits.

In v3 the store is over the wire; tx-sharing is impossible. The acquisition flow becomes:

1. Supervisor opens its own Postgres tx (rimsky-side bookkeeping only).
2. Supervisor claims dispatch row (`UPDATE rimsky_dispatch SET claimed_by = supervisor_id ...`) in the tx — preserves v2 ordering (claim dispatch first, then lock acquisition).
3. Supervisor INSERTs `rimsky_lock_holders` rows with client-generated UUIDs (`claim_id`) and empty `address` in the same tx.
4. Supervisor RPCs `Store.Open(claim_id, ClaimSpec)` over the wire — store creates whatever internal state it needs, returns `ClaimResult`.
5. Supervisor UPDATEs `rimsky_lock_holders.address` with the returned address.
6. Supervisor INSERTs `rimsky_claim_holders` rows for held claims.
7. Supervisor COMMITs the rimsky-side tx.

The store's state mutation (step 4) is in its own transaction on its own data store. Rimsky's bookkeeping mutations (steps 2, 3, 5, 6) are atomic with each other (single rimsky-side tx). The two are decoupled.

### 7.4 Failure modes

| Failure | Rimsky-side state | Store-side state | Recovery |
|---|---|---|---|
| Supervisor crash before step 1 | Nothing | Nothing | None needed |
| Supervisor crash between steps 2–7 (rimsky-side tx never commits) | Nothing persisted | Store may have created state keyed by `claim_id` rimsky discards | Store's own TTL / sweep |
| Step 4 RPC fails | Tx rolled back | Store may or may not have partial state | Store's own TTL / sweep |
| Tx commit fails after step 6 | Nothing persisted | Store has state keyed by `claim_id` rimsky discards | Store's own TTL / sweep |
| Supervisor crash after step 7 (committed) before dispatching executor | Rimsky bookkeeping holds; heartbeat lapses; orphan reaper deletes after `5 × heartbeat_interval` | Store has state keyed by `claim_id` that rimsky reaps | Store's TTL / sweep, OR rimsky-driven `Abandon` (see §7.5) |

In every failure case where rimsky's bookkeeping diverges from store state, recovery is the store's responsibility via its own TTL / sweep mechanism. Rimsky does not require the store to participate in distributed transactions.

### 7.5 Orphan-driven cleanup (optional optimization)

When rimsky's orphan reaper deletes a stale `rimsky_lock_holders` row, it MAY first RPC `Store.Abandon(claim_id, region, address)` to give the store immediate notice. This is an optimization — the store's own TTL / sweep handles the case where Abandon is not called or fails. (Idempotency in `claim_id` is required for all terminal verbs unconditionally per §7.8 obligation #3, so the store handles a duplicate `Abandon` call as a no-op.)

The default v3 behavior: rimsky's orphan reaper does NOT call `Abandon` first — it deletes the lock-holder row and trusts the store's TTL to handle cleanup. The Abandon-first optimization is left to a future cycle if store cleanup latency becomes a problem.

### 7.6 Single-writer-per-region

The structural invariant is preserved by rimsky's bookkeeping: the conflict predicate (region overlap × mode coexistence per the C3.1 matrix) runs against `rimsky_lock_holders` in rimsky's own tx, and the INSERT for a new claim only succeeds if no incompatible row exists. Whether the store has partial state for a discarded acquisition is irrelevant to single-writer-per-region; the store's TTL handles its own cleanup.

The invariant holds because the lock-holder row INSERT is gated on the conflict predicate over `rimsky_lock_holders` only — rimsky never consults store state for conflict detection. Store orphan state (the failure rows in §7.4) is invisible to the conflict predicate because it isn't recorded in `rimsky_lock_holders`. A second supervisor attempting to claim the same region after the first's failure sees no conflicting lock-holder row and proceeds; the store's `claim_id` (different for the new attempt) keys a fresh store-side state independent of the orphaned one.

### 7.7 Region conflict semantics — byte-equal canonicalization

In v2, the in-process `Store` interface exposed two pure methods called by the supervisor's conflict predicate every eligibility tick:

```go
RegionsConflict(a, b []byte) bool
UnmarshalRegion(raw []byte) ([]byte, error)
```

Each store implemented these to express its own region-overlap semantics (postgres: byte-equal on item IDs; filesystem: glob-overlap on path patterns).

**v3 removes both methods.** Rimsky's conflict predicate uses byte-equal comparison of `region` bytes. There is no protocol verb for conflict detection; rimsky executes byte-equal locally against `rimsky_lock_holders.region_data` in the same tx that does the INSERT.

**Store obligation: canonicalize regions for byte-equal correctness.** The store's `region` field returned from `Open` must be canonical bytes such that two claims that should conflict produce byte-equal regions, and two claims that should NOT conflict produce byte-different regions. The store is the only entity that knows its own conflict semantics; canonicalization is its job.

**Standard-store implications for v3:**

- **`stores/postgres/`**: regions are item identifiers (store-chosen). Byte-equal works trivially — different items have different IDs.
- **`stores/filesystem/`**: supports **concrete paths only**, not glob selectors. v2's glob support is dropped from the v3 standard filesystem store. Two claims on the same path produce byte-equal regions; two claims on different paths produce byte-different regions. Operators who need glob semantics write a custom store-service that canonicalizes globs to a "lock-set descriptor" returned from `Open` (e.g., a hash over the resolved file set, or a canonical pattern form) — but the standard `stores/filesystem/` does not implement this.
- **`stores/stub/`**: trivially supports byte-equal (regions are whatever the test fixture wires up).

**Why this is sufficient:** the v2 spec scope (row B6) already noted that selectors are dynamically substituted at dispatch time, defanging deploy-time validation against store grammars. The v2 design was already byte-equal-comparable for the most common case (pick-policy item IDs). The v3 simplification formalizes "byte-equal is the wire-level conflict semantic; canonicalization is the store's responsibility" — and accepts that v3 standard filesystem loses glob support as a clean break (pre-v1, break freely).

**Multi-lock sort order (v2 §13.7).** v2's atomic acquisition sorts locks by `(lock_kind, lock_name | (store_name, region_data_canonical))` to prevent deadlock under contention. v3 keeps the same sort order; the "canonical" form is now simply the raw `region_data` bytes the store returned from `Open`. No separate canonicalization step is required — store-returned bytes ARE the canonical form.

**Future option (deferred):** if a future cycle motivates store-defined conflict semantics richer than byte-equal, the path is either (a) a store-side `RegionsConflict` RPC (cacheable; deferred from v3), or (b) a store-shipped region-grammar descriptor in `Capabilities()` that rimsky executes locally. Both are non-breaking additions to the v3 protocol; v3 ships the byte-equal foundation only.

### 7.8 Store-author obligations

The decoupled atomicity model (§7.3) and the byte-equal conflict model (§7.7) place real obligations on the store that v3 supervisors rely on. These are non-negotiable.

1. **Stores MUST implement a sweep / TTL mechanism that reclaims state created by an `Open` whose response was lost or whose corresponding rimsky-side tx never committed.** Without this, orphaned store state accumulates indefinitely. The mechanism is store-internal — periodic goroutine, cron job within the store-service binary, on-demand reclamation at the next `Open`, etc. v3 does not prescribe how; it only requires that orphaned state cannot pile up.

2. **The sweep keys on `claim_id`.** Stores SHOULD record `claim_id` before any state mutation in `Open` so the sweep can identify orphans by "this `claim_id` is older than the sweep TTL and rimsky has not subsequently issued `Commit` / `Abandon` / `Release` for it."

3. **All terminal verbs (`Commit` / `Abandon` / `Release` / `Delete`) MUST be idempotent in `claim_id`.** Rimsky may retry these at the supervisor level on transport errors, so a store may receive the same call twice for the same `claim_id`. The second call is a no-op.

4. **Stores MUST NOT depend on rimsky calling `Abandon` for orphan cleanup.** Per §7.5, the default v3 behavior is that rimsky's orphan reaper deletes `rimsky_lock_holders` rows without RPCing `Abandon` first. Stores that assume otherwise will leak state.

5. **Stores MUST canonicalize `region` bytes returned from `Open` such that byte-equal comparison correctly indicates conflict** (per §7.7). Two claims that should conflict produce byte-equal `region`; two claims that should not conflict produce byte-different `region`. This is the load-bearing contract for invariant 4b (single-writer-per-region) under the v3 protocol.

These obligations are documented in `docs/store-author-guide.md` (§13.1) as the store-author contract.

## 8. Standard store implementations

Three binaries, each in its own directory under `stores/`:

### 8.1 `stores/filesystem/`

- `cmd/main.go` — entry point; loads its YAML config; constructs the in-process store; starts the gRPC + HTTP servers.
- `server/` — gRPC + HTTP handlers; calls into the store-internal logic.
- `store/` — store-internal logic (file I/O, atomic-rename publish, `pick_policies` if any).
- `Dockerfile.filesystem`.
- `config-example.yml` — operator-facing example.

`write_semantics`: `direct` only in v3 (a future cycle could add `staged_blocking` via atomic-rename publish).

**Selector grammar:** concrete paths only. v2's glob support is dropped per §7.7 (rimsky-side byte-equal conflict requires store canonicalization; glob-overlap canonicalization is out of scope for the v3 standard filesystem). Two claims on the same path conflict; two claims on different paths do not.

### 8.2 `stores/postgres/`

- `cmd/main.go`, `server/`, `store/` mirroring the structure above.
- Store-internal logic includes the items-table machinery for pick-policy claims, the visibility-timeout sweep (store-internal goroutine, replacing rimsky's stripped sweep), the staging-and-flip logic.
- `Dockerfile.postgres`.

`write_semantics`: `direct`.

### 8.3 `stores/stub/`

- Same structure.
- Minimal in-memory state; `Open` returns deterministic addresses; `Commit` / `Abandon` / `Release` are no-ops or update in-memory state for test assertions.
- Used by test fixtures and by anyone who wants a "store-shaped thing that behaves predictably."
- `Dockerfile.stub`.

### 8.4 Lifecycle (programmatic startup)

Each store-service binary's `cmd/` package exposes a `Run(cfg, listener) error` function that the binary's `main()` and test fixtures both call. `main()` loads config from YAML, opens listeners on configured ports, calls `Run`. Test fixtures construct `cfg` programmatically, open ephemeral listeners, call `Run` on a goroutine, and pass the listener address back to the calling test.

This is the standard Go server pattern; it's the only seam needed for the test-fixture loopback (§9).

### 8.5 Directory layout

Each store-service follows the existing `core/cmd/<binary>/main.go` and `executors/<name>/cmd/...` precedent: `cmd/` for the binary entry point, `server/` for RPC handlers, `store/` for store-internal logic, `testfixture/` for the loopback helper. Measured from the `stores/` package root, this is two levels of nesting (`stores/<kind>/<subdir>/`), within the cold-read cheatsheet's "max 2 levels" guideline. The structure separates concerns (binary entry / handlers / store-internal logic / test fixture) without flattening unrelated code into a single directory.

## 9. Test strategy

Three test patterns, each used at the layer where its tradeoffs fit:

### 9.1 Direct in-process bypass — for unit tests

Unit tests in `core/...` that don't care about the wire use a Go-only fake `Store` that satisfies the rimsky-side interface in-Go. No gRPC, no port allocation, no sub-second startup overhead. Used for tests of rimsky-side logic in isolation (eligibility predicate, conflict matrix, auto-terminal aggregation, attribute substitution against fake claim results, etc.).

A small `core/store/storetest/` package provides the fake.

### 9.2 In-process loopback gRPC — for scenario and smoke tests

Scenario tests in `test/scenarios/` and the in-process smoke fixture in `test/smoke/` start the store-service binary's `Run(cfg, listener)` function on a goroutine, listening on an ephemeral port. Rimsky processes (also in-process) dial the loopback address. Real gRPC over the wire; real protocol; sub-second startup.

A test-side helper per store (`stores/<kind>/testfixture/`) wraps the loopback startup so scenario tests don't reimplement it. The helper returns the listener address and a teardown closure.

Postgres for rimsky's own bookkeeping continues to use testcontainers as today.

### 9.3 Real containers — for docker smoke

The docker-smoke verification (formerly T55) uses the full `deploy/docker-compose.yml` stack with all services as separate containers, communicating via service-name DNS. This is the only test layer that exercises real cross-process boundaries (env-var ingestion, container DNS, callback advertise-host wiring, etc.).

Run on demand (not in CI fast-path) because of the image-build and container-startup cost.

### 9.4 Test coverage

The scenario test suite is rebuilt on top of the loopback fixture. The plan-spec'd scenarios from `docs/history/2026-04-27-store-protocol-inertness-cleanup.md` (held-claim invariant tests, inertness audit, single-writer-per-region, atomic acquisition, etc.) land against the loopback wire path.

## 10. Smoke fixture

The in-process smoke fixture (`test/smoke/`) is rewritten:

- Brings up testcontainers Postgres for rimsky's own bookkeeping.
- Starts loopback gRPC + HTTP servers for `store-filesystem`, `store-postgres`, `store-stub` on ephemeral ports.
- Builds rimsky-side `stores.yml` programmatically with the loopback endpoints.
- Starts in-process `rimsky-supervisor`, `rimsky-scheduler`, `rimsky-control-api` against the same Postgres.
- Drives the existing 4-node template (claim-topic / scope / draft / review) end-to-end with 100 force-fires.

The pipeline shape is unchanged; only the wiring changes (each store-service is a goroutine instead of a Go subpackage). One audit item: the existing 4-node template's filesystem-store usage must be checked against §7.7's glob removal — any glob selector in the template needs rewriting to a concrete path before v3 implementation lands. Same audit applies to the docker-compose smoke fixture's templates.

The docker-compose smoke (`deploy/docker-compose.yml`) is rewritten:

- Adds three new services (`store-filesystem`, `store-postgres`, `store-stub`) with their own images and configs.
- Updates the rimsky processes' `RIMSKY_STORES_CONFIG` to point at the new gRPC endpoints (using compose service-name DNS).
- The `init-items` one-shot moves to `stores/postgres/`'s domain (the postgres store-service is responsible for its own items-table provisioning, either via its own one-shot or at first connect).

Both smoke variants verify the full claim acquisition + auto-terminal + held-claim resolution + multi-pick-policy paths.

## 11. Code surfaces affected

### 11.1 Removed (entirely)

- `core/store/registry.go::Factory` interface.
- `core/store/registry.go::StoresConfig` type.
- `core/store/registry.go::Registry.BuildAll`.
- `core/store/registry.go::Registry.Register`. (No factories to register.)
- `core/store/interface.go::Store.Kind()` method. With no per-kind dispatch in rimsky, the kind discriminator has no consumer. Removed.
- `core/store/interface.go::Store.RegionsConflict(a, b []byte) bool`. Replaced by rimsky-side byte-equal comparison; store canonicalizes per §7.7.
- `core/store/interface.go::Store.UnmarshalRegion(raw []byte) ([]byte, error)`. Replaced by rimsky-side byte-equal comparison; store ensures `region` bytes are already canonical when returned from `Open`.
- `core/store/tx.go` — `WithTx`, `TxFromContext`, the entire tx-sharing mechanism.
- `core/store/filesystem/` (subpackage) — moves to `stores/filesystem/`.
- `core/store/postgres/` (subpackage) — moves to `stores/postgres/`.
- `core/store/stub/` (subpackage) — moves to `stores/stub/`.
- `core/controlapi/admin_claim_stores.go` — admin items endpoint.
- `core/controlapi/app.go` — the route registration for the admin items endpoint.
- `core/scheduler/sweep_locks.go::reapItemsTable` (or equivalent visibility-timeout sweep that walks store items tables) — entirely removed. The remaining function in this file (`reapRegionRow`, the orphan reap on `rimsky_lock_holders`) is **modified**, not removed — see §11.2.
- `core/node/template_validator.go::RegistryHooks.IsPickPolicySelector` — the pick-policy validator hook.
- `core/controlapi/templates.go::validatorHooksFor` — the pick-policy hook construction.

### 11.2 Modified

- `core/store/interface.go` — `Store` interface: gains `claim_id` parameter on every verb; gains `Capabilities()` method; loses `Kind()`, `RegionsConflict`, and `UnmarshalRegion`. Final shape is the 5 runtime verbs + `Capabilities()` only.
- `core/store/types.go` — `ClaimSpec` unchanged; `ClaimResult` unchanged; new `ClaimID` type alias for clarity.
- `core/store/registry.go` — `Registry` simplified to a name → `Store` map populated externally (cmd binary at startup). `Get(name)` and `Stores()` accessors stay.
- `core/store/lockholders.go` — `id` UUID now generated client-side at INSERT time (column default kept as a safety net).
- `core/store/conflict.go` — extended with the byte-equal region-conflict predicate (per §7.7). `ModeCoexists` stays unchanged; a new `RegionsByteEqual(a, b []byte) bool` (or similar) becomes the rimsky-side implementation of the conflict comparison that v2 delegated to the store's `Store.RegionsConflict`. Used by `core/queue/postgres/queue.go`'s eligibility predicate and by `core/supervisor/runner_acquire.go`'s pre-INSERT conflict check.
- `core/store/doc.go` — vocabulary update; reference to `core/store/remote/` as the only concrete impl in rimsky.
- `core/cmd/rimsky-supervisor/main.go`, `core/cmd/rimsky-scheduler/main.go`, `core/cmd/rimsky-control-api/main.go` — load the new `stores.yml` schema; build remote clients per entry; call `Capabilities()`; populate the `Registry`. No more `Factory{}` instances; no more in-process `*pgstore.Store` references.
- `core/config/supervisor.go`, `core/config/scheduler.go`, `core/config/controlapi.go` — drop the `StoreFactories` field; rewrite the `Stores` field from `store.StoresConfig` (the v2 map of store-construction config) to a thin "name → endpoint + declared capabilities" form (or equivalent loaded from the new `stores.yml`). The `buildStoreRegistry` helper is replaced by a `dialRemoteStores` helper that, for each entry, dials the gRPC endpoint, calls `Capabilities()`, validates against declared, populates the `Registry` with a remote client.
- `core/supervisor/runner_acquire.go` — verb calls go through the remote client; `Open` is still called inside the rimsky-side tx scope (between BEGIN and COMMIT, per §7.3 step 4) but the store now runs in its own tx independently — no more `TxFromContext` plumbing into the store. Sequence per §7.3.
- `core/supervisor/runner_terminal.go`, `runner_terminal_outcome.go`, `auto_terminal.go` — verb calls go through the remote client; store-side state is independent of rimsky-side bookkeeping tx.
- `core/scheduler/scheduler.go` — drop the visibility-timeout sweep call site.
- `core/scheduler/sweep_locks.go::reapRegionRow` — keep the lock-holder row DELETE; remove the `Store.Abandon` call (per §7.5: orphan reaper does not RPC `Abandon`; store's TTL handles cleanup).
- `core/queue/postgres/queue.go` — eligibility predicate still queries `rimsky_lock_holders` joined against dispatch (rimsky-side only); the conflict portion of the predicate now uses byte-equal comparison on `region_data` (replaces v2's per-store `RegionsConflict` calls — see §7.7). No change to the join shape, only to what the conflict comparison does.
- `proto/v1/node_executor.proto` — no change (executor protocol is independent).
- All affected tests — rewritten or adapted for the new shape.
- All docs — see §13.

### 11.3 Added

- `proto/v1/store_service.proto` — the new store-service protocol definition.
- `proto/v1/gen/store_service.pb.go`, `proto/v1/gen/store_service_grpc.pb.go` — generated bindings.
- `core/store/remote/` — the gRPC client that satisfies the rimsky `Store` interface by making RPCs.
- `core/store/storetest/` — the unit-test fake `Store` impl.
- `stores/` (top-level directory) — sibling to `executors/`.
- `stores/filesystem/cmd/main.go`, `server/`, `store/`, `testfixture/`, `Dockerfile.filesystem`, `config-example.yml`.
- `stores/postgres/cmd/main.go`, `server/`, `store/`, `testfixture/`, `Dockerfile.postgres`, `config-example.yml`.
- `stores/stub/cmd/main.go`, `server/`, `store/`, `testfixture/`, `Dockerfile.stub`, `config-example.yml`.
- `deploy/docker-compose.yml` — three new service entries.
- `deploy/build-images.sh` — three new image-build invocations.

## 12. Schema

No SQL schema changes for v3.

The single adjustment is operational: `rimsky_lock_holders.id` is now generated client-side by the supervisor before INSERT (so it can be passed to `Open` as `claim_id` in the same code path that does the INSERT). The column default (`gen_random_uuid()`) stays as a safety net for any code path that forgets to supply an id.

## 13. Documentation pass

### 13.1 Comprehensive rewrites

- **`docs/protocol.md`** — replace the v2 in-process Store interface description with the v3 wire-protocol shape: 5 + 1 verbs, request/response messages, gRPC + HTTP+JSON encoding, error mapping. Authoritative source remains `proto/v1/store_service.proto`.
- **`docs/architecture.md`** — update the process-topology section: stores are now separate processes; `core/store/` no longer contains store impls; `core/store/remote/` is the only concrete impl in rimsky; the `stores/` directory is the new home for store implementations.
- **`docs/store-author-guide.md`** — full rewrite. Authoring a store now means: implement the 5 + 1 RPC handlers, define your own config schema, ship a binary + Dockerfile + config example. The five store-author obligations from §7.8 are the contract (sweep / TTL for orphan reclamation; `claim_id` keyed orphan identification; idempotent terminal verbs; no dependence on rimsky `Abandon`; **canonical `region` bytes for byte-equal conflict correctness — §7.7**). Plus the auth-blind advisory (§13.3) and the no-store-side-serialization rule (invariant 9b restatement).
- **`docs/operator-guide.md`** — §8.4 (stores config; was §3.4 before the 2026-05-02 CLI/compose section-renumber) rewrites for the new `stores.yml` shape (name → endpoint + declared capabilities). §10.5 (admin endpoints; was §5.5) loses the items endpoint section; in its place, §8.4 gains a "**Runtime item seeding for pick-policy stores**" subsection. The replacement workflow: each store-service that supports pick policies owns its own admin surface (out of scope for the rimsky 5+1 protocol — the store-service may expose its own HTTP admin port, or rely on direct SQL against the store, or some other store-author choice). The reference postgres store-service ships with a documented admin endpoint for items insertion (separate from rimsky's gRPC endpoint, on its own listener port). Operators configure their item-seeding tooling to talk to the store-service directly, never through rimsky.

### 13.2 Lighter touches

- **`docs/executor-author-guide.md`** — minor: the executor↔storage data path is unchanged; the `address` field semantics need a one-liner clarifying that addresses now come from remote store-services rather than in-process code (no behavioral change for the executor).
- **`docs/node-graph-design.md`** — minor: the DSL is unchanged; only the deployment description in §4 needs updating (the "operator runs rimsky processes" picture gains store-services as siblings).
- **`docs/glossary.md`** — minor: confirm "store" / "store implementation" / "store-service" are used consistently per the v2 naming (J4 row).

### 13.3 Auth-blind doc reduction

The v2 documentation pass over §14 (auth) was over-elaborated — it described "encrypt-before-pass" as a primary defense, included extensive prose on asymmetric vs symmetric encryption, layered defense, field-level encryption, reference helper libraries. Per the v3 clarification, that framing is wrong: rimsky has no auth machinery, period; encrypt-before-pass is just user-facing guidance, not a featured concept.

The v3 doc reduction: replace the elaborated prose in `docs/store-author-guide.md`, `docs/executor-author-guide.md`, and `docs/operator-guide.md` with a brief advisory section in each guide. Suggested wording (paraphrase):

> **Rimsky is auth-blind.** Rimsky has no machinery for credentials, encryption, key management, or access control. If sensitive data passes through claim content (payload, address, region) or attribute values, encrypt it before handing it to rimsky. Rimsky transports the bytes opaquely — and persists them in `rimsky_node_attributes` for as long as downstream nodes need them — but does not introspect or protect them. Operator-deployment auth (mTLS, service mesh, IAM) lives in the deployment layer; rimsky has no protocol surface for it.

That's the entire scope of auth-blind documentation in v3. The blessed-invariant-20 annotations stay (they are the structural enforcement of inertness and live on `core/store/types.go::ClaimResult` and `core/attributes/substitution.go::walkPath`); the documentation-side scaffolding around them shrinks.

**Preserve the v2 §17.5 distinction.** The auth-blind reduction must NOT collapse the boundary between **claim content** and **store-config bytes**. Claim content (payload, address, region returned from `Open`) is under invariant 20: rimsky reads it only via `walkPath` for substitution-leaf extraction, never logs/transforms/validates. Store-config bytes (operator-managed; e.g., DSNs in the store's own config YAML, items-table names, pick-policy parameters) are NOT under invariant 20 — they are operator-owned config the store parses internally; rimsky never sees them at all in v3 (per §6.3). The store-author guide should retain this distinction so implementers don't conclude their store's config-loading also has to be inert.

### 13.4 CHANGELOG

`CHANGELOG.md` — append an Unreleased bullet referencing this spec.

### 13.5 CLAUDE.md

Update the gotchas list:

- Invariant 10 redesign: store owns its tx; rimsky's bookkeeping atomicity is now decoupled from store state mutation. The tx-sharing mechanism via `WithTx` / `TxFromContext` is gone.
- New deployment topology: stores are separate processes; rimsky processes dial them at startup.
- New `RIMSKY_STORES_CONFIG` schema: thin "name → endpoint + declared capabilities" form (per §6.1). All three rimsky processes still load it; fail-fast `Capabilities()` handshake at startup against each declared store-service.
- The 4 inertness violations (admin endpoint, validator hook, scheduler sweep, store-only methods on `*pgstore.Store`) are gone — structurally impossible.
- The `Factory` / `Registry.BuildAll` / `StoresConfig` machinery is gone.
- The held-claim resolution gotcha (`auto_terminal.go::CheckAndFireResolution`) is conceptually unchanged in v3 but mechanically updated: store verb calls now go through the remote-client gRPC path, and the store-side action runs in its own tx (no longer shares a tx with the lock-holder DELETE).
- **Region conflict is byte-equal.** The conflict predicate compares `region_data` bytes directly; stores are responsible for canonicalizing regions so byte-equal correctly indicates conflict (§7.7). v2's `Store.RegionsConflict` and `Store.UnmarshalRegion` methods are removed.
- **`rimsky_lock_holders.id` is generated client-side** by the supervisor before INSERT (so it can be passed to `Open` as `claim_id`). The column default `gen_random_uuid()` stays as a safety net; any future code path that touches this INSERT must continue to supply the id explicitly.
- **Blessed invariants updated.** Invariant 10 clarified (semantic preserved on rimsky side; store atomicity decoupled), invariant 14 retired, invariant 15 revised. See spec §4.10. Existing `@blessed-invariant {10,14,15}` annotations in source need updating: 14 deleted, 10 and 15 reworded.

## 14. What stays (carried forward unchanged)

- The 5-verb runtime protocol, claim/named-lock split, region/selector model, capability struct shape (one field), inertness invariant 20, frame interaction, auto-terminal mechanism, held-claim semantics with inheritance, two propagation modes (value-pass / claim-pass), conflict predicate (region overlap × mode coexistence per the C3.1 matrix), single-writer-per-region.
- `core/store/conflict.go::ModeCoexists` — pure rimsky-side logic.
- `core/store/lockholders.go` — rimsky-side bookkeeping helpers.
- The `rimsky_lock_holders`, `rimsky_claim_holders`, `rimsky_dispatch`, `rimsky_nodes`, `rimsky_frames` schemas — no changes for v3.
- The graph DSL surface (`stores:`, `locks:`, `inherits:`, `claim_resolutions:`, attribute schemas).
- The executor protocol (`proto/v1/node_executor.proto`).
- Frame-resolution machinery (`docs/specs/2026-04-26-frame-resolution-design.md`).
- Pre-v1 break-freely rules (`.claude/rules/rules.md`).

## 15. Out of scope (deferred)

These items are explicitly NOT in v3. They are listed here so that future readers don't infer they were forgotten.

| Item | Why deferred |
|---|---|
| Multi-tenant store provisioning | Control-layer concern; spec at `docs/2026-04-26-control-layer.md`. |
| Control-layer auth | Separate cycle; rimsky stays auth-blind in v3. |
| Encrypt-before-pass machinery (any rimsky-side encryption support) | Permanently out — rimsky has no encryption mechanism, full stop. |
| Bridge framework / polyglot SDKs for store-service authors | Permanently out — rimsky specifies the protocol; implementers handle SDKs. |
| `staged_async` standard store | Protocol supports it; no v3 standard store exercises it. Future cycle if a store motivates it. |
| `core/queue/DispatchQueue` pgx leak | Platform-state-backend pluggability axis; addressed when shipping a non-postgres backend. Different scope. |
| Bridge protocol for ecosystem (third-party store-services published via package registry) | Future cycle once v3 stabilizes and there are external authors. |
| Inertness audit beyond what's already done | v2 swept the in-process surfaces; v3's structural strip removes the remaining surfaces (admin endpoint, validator hook, etc.). No further audit owed. |
| Docker smoke as part of CI fast-path | Run on demand; container startup is too slow for per-PR. |

---

**End of spec.** Ready for plan + implementation.
