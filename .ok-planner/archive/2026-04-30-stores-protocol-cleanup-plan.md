# Stores Protocol Cleanup Implementation Plan

**Goal:** Excise substrate-internal vocabulary from the rimsky↔store wire surface and rimsky source: drop `claim_resolutions`, drop `policy_override`, drop the `Delete` wire verb, replace the implicit pool-empty signal with an explicit `OpenResponse` oneof.

**Architecture:** Four coordinated changes to a Go monorepo: a proto change drives Go interface changes, which drive substrate-impl + rimsky-side + test cascades. The wire protocol shrinks from 5+1 to 4+1 verbs. After this cycle, substrate disposition is governed entirely by per-substrate config; rimsky carries only the success/failure binary.

**Tech Stack:** Go (root module `github.com/fallguy/rimsky`, go.mod at repo root), Protobuf 3 + grpc-go, jackc/pgx/v5, testcontainers-go, golangci-lint. Spec: `docs/specs/2026-04-30-stores-protocol-cleanup-design.md`.

**Sequencing rationale:** Proto first — every Go interface change depends on regenerated bindings. Then `core/store/` types (Store interface, OpenOutcome). Then the gRPC client adapter and substrate impls (these compile in isolation against the new types, but rimsky-side callers are still broken until the supervisor edits land). Then rimsky-side callers. Then template-grammar removal. Then tests, scenario fixtures, smoke. Then docs. Then full verification.

**Working tree state at plan start:** `main` already carries the v3 cycle and the http-node fix (committed or staged; this plan ignores both). Plan output is working-tree edits and verification commands only — the user commits when ready.

---

## Task 1 — Proto change

**Files:**
- `proto/v1/store_service.proto`

**Steps:**

1. Edit `proto/v1/store_service.proto`. Make the following changes verbatim:

   a. In the file header docstring (lines 1-17), drop the `policy_override` mention from the field-shapes paragraph:

      Replace `selector, intent, alias, store_name, policy_override are strings.` with `selector, intent, alias, store_name are strings.`.

   b. In the `service StoreService { ... }` block, delete the `Delete` RPC and its docstring (lines 53-55):

      ```proto
        // Delete removes the live region. Regional claims only — pick-policy
        // claims express deletion via Commit + policy_override = "delete".
        rpc Delete(DeleteRequest) returns (DeleteResponse);
      ```

   c. Update the `Commit` and `Abandon` RPC docstrings to drop pick-policy references:

      Replace:
      ```
        // Commit publishes staging into live (staged_blocking rw) or
        // applies the on_commit policy (pick-policy claims). For direct rw,
        // typically a no-op.
        rpc Commit(CommitRequest) returns (CommitResponse);

        // Abandon discards staging or applies the on_give_up policy. For
        // direct rw, typically a degenerate no-op. Not called for read-only
        // claims and not called by the orphan reaper in v3 default behavior
        // (per spec §7.5).
        rpc Abandon(AbandonRequest) returns (AbandonResponse);
      ```
      With:
      ```
        // Commit signals that the consumer of the claim succeeded. The
        // substrate decides what to do with its own state (publish staging
        // into live, delete the items-table row, release-to-back, etc.) per
        // the substrate's own configuration. For direct rw, typically a
        // no-op.
        rpc Commit(CommitRequest) returns (CommitResponse);

        // Abandon signals that the consumer of the claim failed. The
        // substrate decides what to do with its own state per its own
        // configuration. Not called for read-only claims and not called by
        // the orphan reaper in v3 default behavior (per spec §7.5).
        rpc Abandon(AbandonRequest) returns (AbandonResponse);
      ```

   d. Replace the `OpenResponse` message and add the `Acquired` / `Unavailable` messages. Replace:

      ```proto
      // OpenResponse bundles the three opaque-bytes outputs of acquisition.
      // All three may be empty: the all-empty case is the pool-empty signal
      // for pick-policy claims (per spec §4.7).
      message OpenResponse {
        bytes address = 1;
        bytes payload = 2;
        bytes region = 3;
      }
      ```
      With:
      ```proto
      // OpenResponse signals acquisition outcome. Substrates that always
      // have a claim to give return Acquired. Substrates that may have
      // nothing right now (e.g. an empty items-table queue) return
      // Unavailable; rimsky retries on the next scheduler tick. Substrate-
      // side faults flow as gRPC error status codes, not as an Unavailable
      // response.
      message OpenResponse {
        oneof result {
          Acquired    acquired    = 1;
          Unavailable unavailable = 2;
        }
      }

      // Acquired carries the substrate's acquisition outputs. Address,
      // payload, and region are opaque bytes per blessed invariant 20;
      // any or all may be empty depending on the (write_semantics, intent)
      // combination.
      message Acquired {
        bytes address = 1;
        bytes payload = 2;
        bytes region  = 3;
      }

      // Unavailable signals "no claim available right now." No fields.
      message Unavailable {}
      ```

   e. In `CommitRequest` and `AbandonRequest`, drop `string policy_override = 4;`. Final shapes:

      ```proto
      message CommitRequest {
        string claim_id = 1;
        bytes  region   = 2;
        bytes  address  = 3;
      }

      message CommitResponse {}

      message AbandonRequest {
        string claim_id = 1;
        bytes  region   = 2;
        bytes  address  = 3;  // may be empty; store identifies state by claim_id
      }

      message AbandonResponse {}
      ```

   f. Delete the `DeleteRequest` and `DeleteResponse` messages entirely (the two-message block currently sitting between AbandonResponse and ReleaseRequest).

2. Regenerate proto bindings:
   ```sh
   make proto-gen
   ```

**Verification:**
```sh
git diff --stat proto/v1/
```
Expect: `proto/v1/store_service.proto` and `proto/v1/gen/*.pb.go` modified. The generated files will show `Delete` symbols removed and the new `Acquired`/`Unavailable` types added.

```sh
go build ./proto/v1/gen/...
```
Expect: clean build.

---

## Task 2 — Core store types and Store interface

**Files:**
- `core/store/types.go`
- `core/store/interface.go`
- `core/store/doc.go`

**Steps:**

1. In `core/store/types.go`, locate the `ClaimSpec` type's docstring (lines ~39-45) that references `policyOverride` and `claim_resolutions`. Rewrite the docstring to describe the post-cleanup model: substrate disposition is governed by per-substrate config; rimsky carries only success/failure. Keep the type's field shapes unchanged.

2. In `core/store/types.go`, append a new `OpenOutcome` type definition (placement: directly after the existing `ClaimResult` type):

   ```go
   // OpenOutcome is the rimsky-side discriminator that mirrors the
   // OpenResponse oneof on the wire. Available == true means the
   // substrate returned Acquired{...}; Available == false means
   // Unavailable{}. ClaimResult is populated only when Available is
   // true; its fields remain opaque json.RawMessage bytes per blessed
   // invariant 20.
   type OpenOutcome struct {
       Available bool
       Result    ClaimResult
   }
   ```

3. In `core/store/interface.go`, modify the `Store` interface:

   a. Change `Open`'s return type from `(ClaimResult, error)` to `(OpenOutcome, error)`.

   b. Drop the trailing `policyOverride string` parameter from `Commit` and `Abandon`.

   c. Delete the entire `Delete(ctx context.Context, claimID ClaimID, region []byte) error` method.

   d. Update the interface's package-doc comment block (the lines preceding the `type Store interface {`) to drop any mentions of `Delete`, `policy_override`, or pick-policy action vocabulary, and to reflect "4 runtime verbs + 1 startup handshake."

4. In `core/store/doc.go` (lines ~41-49), the package-level doc lists "Five protocol verbs (spec §4.1)" with `Delete` and `policyOverride`. Rewrite that block to "Four protocol verbs (spec §4.1)", drop the `Delete` bullet, and remove `policyOverride` mentions from the Commit / Abandon bullet text. Reference the cleanup spec at `docs/specs/2026-04-30-stores-protocol-cleanup-design.md` as the authoritative current contract.

**Verification:**
```sh
go build ./core/store/...
```
Expect: build fails — the package's own subpackages (`remote`, `storetest`) still reference the old surface. That's fine; the immediate target is the parent package's own files.

```sh
go vet ./core/store
```
Expect: clean. The `core/store` package itself (interface.go, types.go, doc.go) compiles in isolation; only the subpackages haven't been updated yet.

---

## Task 3 — gRPC client adapter

**Files:**
- `core/store/remote/client.go`

**Steps:**

1. Open `core/store/remote/client.go`. Locate the `Open` method.

2. Update the method body to map the new `OpenResponse` oneof to `OpenOutcome`. Reference shape (matching spec §4.1):

   ```go
   resp, err := c.grpc.Open(ctx, req)
   if err != nil {
       return store.OpenOutcome{}, err
   }
   if u := resp.GetUnavailable(); u != nil {
       return store.OpenOutcome{Available: false}, nil
   }
   acq := resp.GetAcquired()
   if acq == nil {
       return store.OpenOutcome{}, fmt.Errorf("Open: response carries neither Acquired nor Unavailable")
   }
   return store.OpenOutcome{
       Available: true,
       Result: store.ClaimResult{
           Address: acq.Address,
           Payload: acq.Payload,
           Region:  acq.Region,
       },
   }, nil
   ```

   Update the `Open` method's signature to return `(store.OpenOutcome, error)`. The function name and exterior call signature otherwise stay.

3. In the same file's `Commit` method, drop the `policyOverride string` parameter. Drop `PolicyOverride: policyOverride` from the constructed `genv1.CommitRequest{...}`. Same change in `Abandon`.

4. Delete the entire `Delete` method on the client struct.

5. If the client carries any internal helpers / type-assertions that referenced the old shape (e.g. an `openResultFromProto` or similar), delete them.

**Verification:**
```sh
go build ./core/store/remote/...
```
Expect: clean build. (`core/store/remote` is a leaf package that imports only `core/store`, `proto/v1/gen`, and stdlib.)

```sh
go test ./core/store/remote/... -count=1
```
Expect: pass. The remote package's test surface is small (mostly a dial-options test); it should not exercise the changed API directly.

---

## Task 4 — Test fake (`core/store/storetest/fake.go`)

**Files:**
- `core/store/storetest/fake.go`

**Steps:**

1. Locate the package doc-comment paragraph (lines ~20-30) that references "an all-empty `ClaimResult` to simulate the pool-empty signal." Rewrite it to describe the new `OpenOutcome` mechanism: the test sets `OpenFunc` to return `OpenOutcome{Available: false}` to simulate the substrate having nothing to give.

2. Change the `OpenFunc` field's type:

   ```go
   // Before
   OpenFunc func(claimID store.ClaimID, spec store.ClaimSpec) (store.ClaimResult, error)
   // After
   OpenFunc func(claimID store.ClaimID, spec store.ClaimSpec) (store.OpenOutcome, error)
   ```

3. In the fake's `Open` implementation, the wrapper calls `OpenFunc` (if non-nil) and returns its outputs verbatim — no shape adjustment needed beyond the type change. If the fake currently constructs a default `ClaimResult{...}` when `OpenFunc` is nil, replace that with a default `OpenOutcome{Available: true, Result: ClaimResult{...}}` (preserving existing default semantics — a real claim with empty bytes).

4. Drop `policyOverride` from the fake's `Commit` and `Abandon` method signatures and from any callback fields (e.g. if the fake has a `CommitFunc` callback typed `func(claimID, region, address, policyOverride) error`, retype to drop the last parameter).

5. Delete the fake's `Delete` method entirely. If the fake records calls in a slice (`Calls []FakeCall` or similar), also remove the `Verb: "delete"` recording paths and the `PolicyOverride` field from the `FakeCall` struct (or whatever recorded-call type is used).

**Verification:**
```sh
go build ./core/store/storetest/...
```
Expect: clean build.

```sh
go test ./core/store/storetest/... -count=1
```
Expect: pass. (Or "no test files" — the package is itself a test fixture.)

---

## Task 5 — Postgres substrate impl

**Files:**
- `stores/postgres/store/store.go`
- `stores/postgres/store/store_test.go`
- `stores/postgres/server/server.go`
- `stores/postgres/cmd/main.go` (if it threads `policyOverride` through)

**Steps:**

1. In `stores/postgres/store/store.go`, change `Open`'s return type to `corestore.OpenOutcome`. Locate every return path:

   - The pool-empty path that previously returned `corestore.ClaimResult{}, nil` on `pgx.ErrNoRows` (in `openPickPolicy` or equivalent). Change it to return `corestore.OpenOutcome{Available: false}, nil`.
   - The success path that returned a populated `corestore.ClaimResult{...}`. Wrap it: `return corestore.OpenOutcome{Available: true, Result: corestore.ClaimResult{...}}, nil`.
   - The regional path (if separate). Same wrap.

2. Drop `policyOverride string` from `Commit` and `Abandon` method signatures. In each method body, the call to `applyPickAction` drops its policy-override argument too — pass through `successPath` only.

3. Modify `applyPickAction` (lines ~237-307):

   - Drop the `policyOverride` parameter from the function signature.
   - Delete the `action := policyOverride; if action == "" { ... }` block. The action is now read directly from `pp.OnCommitDefault` (when `successPath`) or `pp.OnGiveUpDefault` (else).
   - Update the function's docstring (lines ~233-236) to describe the post-cleanup behavior: substrate-side defaults are the only governing input.

4. Delete the entire `Delete` method (lines ~209-215). Delete its docstring.

5. In `stores/postgres/server/server.go`, the gRPC server adapter implements `StoreServiceServer`. Update it:

   - Map the new `OpenResponse` oneof in the `Open` handler: build a `*genv1.OpenResponse` whose `Result` field is set to either `&genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{...}}` or `&genv1.OpenResponse_Unavailable{Unavailable: &genv1.Unavailable{}}` based on the store's `OpenOutcome.Available`.
   - Drop `policy_override` field reads from the `Commit` and `Abandon` handlers.
   - Delete the `Delete` handler method entirely.

6. In `stores/postgres/cmd/main.go`, no behavioral changes are expected (cmd is a thin wrapper around server + store). If it has any code referencing the deleted `Delete` server method or the `policy_override` field, scrub it.

7. In `stores/postgres/store/store_test.go`:

   - Find every test that calls `Commit` or `Abandon` with a non-empty `policyOverride` argument. Drop the argument. Where a test was specifically exercising the override semantics (e.g. "commit with override=delete"), rewrite the test to drive the same scenario through substrate config: instantiate the store with `pick_policies[<selector>].on_commit_default = "delete"` and assert the same end state.
   - Delete any test that uniquely exercised the `Delete` method.
   - Update any test that drove the all-empty-`ClaimResult` pool-empty signal to drive the new path: configure the items table empty, call `Open`, expect `OpenOutcome{Available: false}`.

**Verification:**
```sh
go build ./stores/postgres/...
```
Expect: clean build.

```sh
go test ./stores/postgres/... -count=1
```
Expect: pass. Some tests use testcontainers-go (Postgres). Docker must be running.

---

## Task 6 — Filesystem substrate impl

**Files:**
- `stores/filesystem/store/store.go`
- `stores/filesystem/store/store_test.go`
- `stores/filesystem/server/server.go`

**Steps:**

1. In `stores/filesystem/store/store.go`:

   - Change `Open`'s return type to `corestore.OpenOutcome`. Wrap the existing success-path `ClaimResult{...}` in `OpenOutcome{Available: true, Result: ...}`. The filesystem store has no "unavailable" path — concrete-paths-only — so every success returns `Available: true`.
   - Drop `policyOverride string` from `Commit` and `Abandon` signatures (the filesystem store currently ignores the arg, but the proto-level removal forces the signature change).
   - Delete the `Delete` method (lines ~149-161 — the `s.mu.Lock(); delete(s.claims, claimID); ...` body and the `os.Remove` regional branch).

2. In `stores/filesystem/server/server.go`:

   - Update the `Open` handler to build the new `OpenResponse` oneof.
   - Drop `policy_override` reads from `Commit` and `Abandon` handlers.
   - Delete the `Delete` handler method entirely.

3. In `stores/filesystem/store/store_test.go`:

   - Delete `TestDeleteRemovesTarget` (lines ~146-167) entirely.
   - Delete `TestDeleteEmptyRegionIsNoop` (lines ~169-174) entirely.
   - Update any test that calls `Commit` or `Abandon` with a `policyOverride` argument to drop the argument.

**Verification:**
```sh
go build ./stores/filesystem/...
```
Expect: clean build.

```sh
go test ./stores/filesystem/... -count=1
```
Expect: pass.

---

## Task 7 — Stub substrate impl

**Files:**
- `stores/stub/store/store.go`
- `stores/stub/store/store_test.go`
- `stores/stub/server/server.go`
- `stores/stub/cmd/main.go` (if it threads anything through)

**Steps:**

1. In `stores/stub/store/store.go`:

   - Change `Open`'s return type to `corestore.OpenOutcome`. The stub has an empty-FIFO path that previously returned `ClaimResult{}` (all-empty) — change it to `OpenOutcome{Available: false}`. The success path wraps in `OpenOutcome{Available: true, Result: ...}`.
   - Drop `policyOverride string` from `Commit` and `Abandon` signatures.
   - Delete the `Delete` method (lines ~159-167) entirely. Also remove the `Verb: "delete"` entry from the `Call` struct's call-recording — if `Call.Verb` is a free-form string field, no struct change needed; if it's a typed enum, drop the `delete` case.
   - If the `Call` struct has a `PolicyOverride` field, delete it.

2. In `stores/stub/server/server.go`: same shape changes as the postgres / filesystem servers — `OpenResponse` oneof, drop `policy_override`, drop `Delete` handler.

3. In `stores/stub/cmd/main.go`: scrub any references.

4. In `stores/stub/store/store_test.go`:

   - Update any test that drove the all-empty-`ClaimResult` empty-FIFO signal to expect `OpenOutcome{Available: false}` instead.
   - Drop `policyOverride` arguments from `Commit` / `Abandon` test calls.
   - Delete any test that uniquely exercised the `Delete` method or the `delete` call recording.

**Verification:**
```sh
go build ./stores/stub/...
```
Expect: clean build.

```sh
go test ./stores/stub/... -count=1
```
Expect: pass.

---

## Task 8 — HTTP+JSON bridge handler

**Files:**
- `stores/internal/bridge/bridge.go`

**Steps:**

1. Open `stores/internal/bridge/bridge.go`. Locate the route registration block and the `writeJSON` helper.

2. **Delete the `POST /v1/delete` route registration** and its handler function. If the handler is a method on a `bridgeHandler` struct, remove the method as well.

3. **Drop `policy_override` field decoding** from the `Commit` and `Abandon` handlers. The handlers' `decodeOptional` (or equivalent) call site that pulls `policy_override` from the JSON body goes away. The downstream call to `Commit` / `Abandon` drops the argument.

4. **Update the `Open` handler** to emit the new oneof shape. The handler builds a `*genv1.OpenResponse` either by:
   ```go
   return &genv1.OpenResponse{
       Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
           Address: acq.Result.Address,
           Payload: acq.Result.Payload,
           Region:  acq.Result.Region,
       }},
   }
   ```
   or by:
   ```go
   return &genv1.OpenResponse{
       Result: &genv1.OpenResponse_Unavailable{Unavailable: &genv1.Unavailable{}},
   }
   ```
   based on `outcome.Available`.

5. **Switch `writeJSON` (lines ~170) from `encoding/json` to `protojson`.** Replace the `json.NewEncoder(w).Encode(v)` call with a `protojson.Marshal` invocation:

   ```go
   import "google.golang.org/protobuf/encoding/protojson"
   import "google.golang.org/protobuf/proto"

   func writeJSON(w http.ResponseWriter, v proto.Message) {
       w.Header().Set("Content-Type", "application/json")
       data, err := protojson.Marshal(v)
       if err != nil {
           http.Error(w, "marshal: "+err.Error(), http.StatusInternalServerError)
           return
       }
       _, _ = w.Write(data)
   }
   ```

   Update every `writeJSON(w, respObj)` call site so `respObj` is a `proto.Message` (the `genv1.*Response` types satisfy this — verify by ensuring callers pass pointer types).

6. The pre-switch comment block above `writeJSON` (lines ~159-169) that flagged the `oneof` trigger is now stale. Replace it with a one-line comment: `// writeJSON serializes the response with protojson, the canonical proto3-JSON encoder. Required because OpenResponse uses a oneof that encoding/json does not produce in the proto3-JSON discriminator shape.`

7. Inbound request decoding (`decodeOptional` / `json.Unmarshal` on `req.Body`) does NOT change — no inbound oneofs.

**Verification:**
```sh
go build ./stores/internal/bridge/...
```
Expect: clean build.

```sh
go test ./stores/internal/bridge/... -count=1
```
Expect: pass. If the bridge package has tests that used `encoding/json` to decode the response and assert the all-empty-bytes shape, they need to be rewritten to assert the new oneof shape (decoding with `protojson.Unmarshal` into `*genv1.OpenResponse` and checking `resp.GetAcquired()` / `resp.GetUnavailable()`).

---

## Task 9 — Supervisor `runner_acquire.go::acquireClaim`

**Files:**
- `core/supervisor/runner_acquire.go`

**Steps:**

1. Open `core/supervisor/runner_acquire.go`. Locate the `acquireClaim` function and the all-empty-bytes check (around line 375):

   ```go
   if len(cr.Address) == 0 && len(cr.Region) == 0 && len(cr.Payload) == 0 {
       return AcquiredLock{}, false, nil
   }
   ```

2. Replace the call shape and the check. The existing call to `Store.Open` returned `(ClaimResult, error)`; now it returns `(OpenOutcome, error)`. Rewrite:

   ```go
   outcome, err := s.Open(ctx, claimID, spec)
   if err != nil {
       return AcquiredLock{}, false, fmt.Errorf("acquireClaim: Open(...): %w", err)
   }
   if !outcome.Available {
       return AcquiredLock{}, false, nil
   }
   cr := outcome.Result
   ```

   Everything downstream that used `cr.Address`, `cr.Region`, `cr.Payload` continues to work unchanged.

3. Search the file for any block-comment that referenced "pool-empty" or "all-empty bytes" — rewrite those to describe the explicit `Unavailable` outcome.

**Verification:**
```sh
go build ./core/supervisor/...
```
Expect: build still fails (auto_terminal.go and friends not yet updated). That's expected. Continue.

---

## Task 10 — Supervisor `auto_terminal.go`

**Files:**
- `core/supervisor/auto_terminal.go`

**Steps:**

1. **Delete `selectResolutionAction`** (the function that picks `r.OnCommit` or `r.OnGiveUp`). Lines ~118-132.

2. **Delete `fireResolutionVerb`** (the switch over `commit` / `abandon` / `delete` / `release_to_back` / `release_to_head`). Lines ~134-155.

3. **Rewrite `CheckAndFireResolution`** (lines ~53-116):

   - Drop the `alias string` and `claimResolutions map[string]node.ClaimResolution` parameters from the function signature. The only parameters are `ctx context.Context`, `args RunArgs`, `tx pgx.Tx`, `lockHolderID shared.UUID`.
   - Inside the body, the `resolution := claimResolutions[alias]; verbAction, success := selectResolutionAction(...)` block is gone.
   - Replace the `fireResolutionVerb(...)` call with an inline switch:
     ```go
     var verbErr error
     if anyFailed {
         verbErr = s.Abandon(ctx, claimID, region, address)
     } else {
         verbErr = s.Commit(ctx, claimID, region, address)
     }
     if verbErr != nil {
         return fmt.Errorf("CheckAndFireResolution: substrate verb: %w", verbErr)
     }
     ```
   - Update the function's package-level docstring (the comment block at lines 1-8 and the func docstring at lines 26-52) to describe the simplified routing: success → Commit; failure → Abandon. Drop references to `claim_resolutions`, action vocabulary, and the spec §4.10 invariant 13.1 routing table.

4. The `node` package import is no longer needed — remove it from the import block if no other reference remains.

**Verification:**
```sh
go build ./core/supervisor/...
```
Expect: still fails (callers in runner_terminal.go pass the old arguments). Continue to Task 11.

---

## Task 11 — Supervisor `runner_terminal.go` and `runner_held_claims.go`

**Files:**
- `core/supervisor/runner_terminal.go`
- `core/supervisor/runner_held_claims.go`

**Steps:**

1. In `core/supervisor/runner_terminal.go`:

   a. Locate `releaseClaim` (line ~497-530). Drop the lookup-resolution logic:
      ```go
      // BEFORE
      resolution := resolutionForAlias(acq.NodeDef, spec.Alias)
      verbAction, _ := selectResolutionAction(resolution, success)
      // ... fireResolutionVerb-style call or direct Store call ...
      ```
      Replace with a direct success-vs-failure dispatch:
      ```go
      var verbErr error
      if success {
          verbErr = s.Commit(ctx, claimID, region, address)
      } else {
          verbErr = s.Abandon(ctx, claimID, region, address)
      }
      if verbErr != nil {
          return fmt.Errorf("releaseClaim: substrate verb: %w", verbErr)
      }
      ```
      Keep the surrounding guards (claimant-supervisor-ID check, idempotency-by-claim_id reasoning, lock-holder DELETE).

   b. Update the doc-comment block at line ~451-468 (the `Logical decomposition` block that mentions "Abandon / Delete / release_to_*) per claim_resolutions") to describe the post-cleanup flow.

   c. Locate `releaseInheritedClaimsInTx` (line ~566-600). The current call site (line ~589) is:
      ```go
      if err := CheckAndFireResolution(ctx, args, tx, ia.LockHolderID, ia.Alias, resolutions); err != nil {
      ```
      Replace with:
      ```go
      if err := CheckAndFireResolution(ctx, args, tx, ia.LockHolderID); err != nil {
      ```
      Delete the preceding `resolution, err := resolutionForAcquirerNode(...)` block (line ~582) and the `resolutions` map construction.

   d. Search the file for any other references to `OnCommit`, `OnGiveUp`, `ClaimResolution`, or `claim_resolutions` (line ~602 has `acq.NodeDef.ClaimResolutions`); delete or rewrite them.

2. In `core/supervisor/runner_held_claims.go`:

   - Delete `resolutionForAlias` (lines ~224-232).
   - Delete `resolutionForAcquirerNode` (lines ~235-256).
   - Search for any other references to `ClaimResolutions` / `ClaimResolution` and remove. Update doc-comments that referenced the `claim_resolutions` template grammar.

**Verification:**
```sh
go build ./core/supervisor/...
```
Expect: clean build (auto_terminal.go + runner_acquire.go + runner_terminal.go + runner_held_claims.go all aligned).

---

## Task 12 — Supervisor `terminal_outcome.go` comment

**Files:**
- `core/supervisor/terminal_outcome.go`

**Steps:**

1. Open `core/supervisor/terminal_outcome.go`. The file does not call `CheckAndFireResolution`; it only carries a package-level comment (line 4) referencing "release routing happens per-claim via `claim_resolutions`."

2. Rewrite that comment line (and any surrounding doc-comment context) to describe the post-cleanup model: "release routing fires substrate verbs (Commit on success, Abandon on failure) directly; substrate-side config governs disposition."

**Verification:**
```sh
go vet ./core/supervisor/...
```
Expect: clean.

---

## Task 13 — Template grammar removal

**Files:**
- `core/node/template.go`
- `core/node/inheritance.go`
- `core/node/template_validator.go`
- `core/controlapi/templates.go`

**Steps:**

1. In `core/node/template.go`:
   - Delete the `ClaimResolutions map[string]ClaimResolution \`yaml:"claim_resolutions,omitempty"\`` field on the `Node` (or `TemplateNodeDef`) struct. Line ~60.
   - Delete the entire `ClaimResolution` struct definition (lines ~108-121) including its docstring.
   - Search the file for any remaining references to `ClaimResolution` and remove.

2. In `core/node/inheritance.go`:
   - Delete the entire validation block at lines ~142-182 (`Index nodes by type for ClaimResolutions lookups`, `cr, has := acq.ClaimResolutions[alias]`, the error-emission for held claims missing `claim_resolutions`, and any `nodeIndexByType` / `storeIndexForAlias` helpers that exist only for this block).
   - Update the file's package-level doc-comment (lines ~11-22) to remove references to `claim_resolutions[<alias>]` validation.

3. In `core/node/template_validator.go`:
   - Find the per-field walker branch that walks `ClaimResolutions` (search for "ClaimResolutions" in the file). Delete that branch.
   - Update any related doc-comments.

4. In `core/controlapi/templates.go`:
   - Delete the `ClaimResolutions map[string]claimResolutionJSON \`json:"claim_resolutions,omitempty"\`` field on the `nodeJSON` struct (line ~48).
   - Delete the entire `claimResolutionJSON` struct definition (lines ~68-73).
   - Delete the cross-translation block in the JSON→Go conversion function (lines ~134-141 — `if len(n.ClaimResolutions) > 0 { ... def.ClaimResolutions = ... }`).
   - Update the package doc-comment (line ~9) to drop `claim_resolutions` from the enumerated fields list.

**Verification:**
```sh
go build ./core/node/... ./core/controlapi/...
```
Expect: clean build.

```sh
go test ./core/node/... -count=1
```
Expect: tests will fail because `template_validator_test.go`, `inheritance_test.go`, etc. still reference the deleted types/fields. Note the failures; Task 15 fixes them.

---

## Task 14 — Scenario harness builders

**Files:**
- `core/scenario/harness.go`
- `core/scenario/harness_util.go`
- `core/scenario/harness_test.go`

**Steps:**

1. In `core/scenario/harness.go`:
   - Search for `ClaimResolution`, `ClaimResolutions`, `WithClaimResolution`, `claim_resolutions`. Find the JSON-serialization block that emits `claim_resolutions` in template payloads (around lines 506-515 per prior reviewer note). Delete it.
   - Find any helper builder functions that take a `ClaimResolution` argument; delete them.

2. In `core/scenario/harness_util.go`:
   - Find the option-helper block (around lines 591-600 / 631 per prior reviewer note) that constructs `claim_resolutions` content. Delete it.

3. In `core/scenario/harness_test.go`:
   - Drop any test invocations that exercise the removed builders / option helpers.

**Verification:**
```sh
go build ./core/scenario/...
```
Expect: clean build.

```sh
go test ./core/scenario/... -count=1
```
Expect: pass (or test failures concentrated in obvious removed-builder uses, addressed in Task 15).

---

## Task 15 — Unit-test updates (auto_terminal, template validators, control-api)

**Files:**
- `core/supervisor/auto_terminal_test.go`
- `core/node/template_validator_test.go`
- `core/node/inheritance_test.go` (if it exists; check)
- `core/controlapi/app_test.go`
- `core/controlapi/admin_routes_test.go` (if it carries `claim_resolutions` JSON)

**Steps:**

1. In `core/supervisor/auto_terminal_test.go`:
   - Identify every test that invokes `CheckAndFireResolution`. The current signature was `(ctx, args, tx, lockHolderID, alias, claimResolutions)`. Drop the last two arguments at every call site.
   - The `TestCheckAndFireResolution_AnyFailedFiresGiveUp` test renames its assertions to "fires Abandon" (no more "give_up" template-vocabulary). Same for the commit-fires test.
   - Drop any test setup that built a `map[string]node.ClaimResolution{...}` literal.
   - Add a test (or modify an existing one) that covers the success path expects `Commit` was called with the right `(claimID, region, address)` and the failure path expects `Abandon` with the same.

2. In `core/node/template_validator_test.go`:
   - Drop the test cases that asserted `ClaimResolutions` validation behavior.
   - Add a single new test that asserts a template carrying a `claim_resolutions:` block fails deploy with an unknown-field error (or whatever the YAML/JSON unmarshaler produces for unknown fields — depending on the strictness setting on the deserializer).

3. In `core/node/inheritance_test.go` (if it exists):
   - Drop the test cases that exercised the deleted held-claim claim_resolutions validation block.

4. In `core/controlapi/app_test.go`:
   - Drop test cases that posted templates with `claim_resolutions` JSON content.
   - Update any "deploy succeeds" cases that incidentally included `claim_resolutions` in their fixture body.

5. In `core/controlapi/admin_routes_test.go`:
   - Audit for `claim_resolutions` references; remove if present.

**Verification:**
```sh
go test ./core/supervisor/... ./core/node/... ./core/controlapi/... -count=1
```
Expect: pass.

```sh
go test ./core/supervisor/... -race -count=3
```
Expect: pass (race-sensitive paths per `.claude/rules/rules.md`).

---

## Task 16 — Scenario tests

**Files:**
- `test/scenarios/claim_stores/auto_terminal_aggregate_outcome_test.go`
- `test/scenarios/frame_resolution/held_claim_resolution_at_frame_end_test.go`
- `test/scenarios/stores/` (all tests in this directory; audit each)
- `test/scenarios/locks/regional_conflict_race_test.go` (audit; may not need changes)
- `test/scenarios/attributes/` (audit; may not need changes)

**Steps:**

1. In `test/scenarios/claim_stores/auto_terminal_aggregate_outcome_test.go::TestAutoTerminalAggregateCommitEndToEnd`:
   - Drop the resolution-vocabulary parts of the template fixture (`claim_resolutions` map literal in the template-deploy body).
   - The wire-path assertion must expect `Commit` (not `release_to_back` or any pick-policy string).
   - If the test interrogates the substrate's recorded calls (via the stub fixture's `Calls` slice), update the expected call shape to no longer include `policy_override`.

2. In `test/scenarios/frame_resolution/held_claim_resolution_at_frame_end_test.go`:
   - Drop the resolution-vocabulary parts of the fixture.
   - Assertion: success path → `Commit` fires.

3. In `test/scenarios/stores/`:
   - Audit every `*_test.go` file. Find tests that:
     - Drove the all-empty-bytes pool-empty signal — rewrite to expect `OpenOutcome{Available: false}` or, at the wire layer, an `OpenResponse{Unavailable{}}` shape.
     - Used `claim_resolutions` in their template fixtures — drop.
     - Asserted on `policy_override` in recorded substrate calls — drop the field from assertions.

4. The `test/scenarios/locks/regional_conflict_race_test.go` and `test/scenarios/attributes/` directories likely don't need changes (they cover lock + attribute semantics, not claim resolutions). Verify by grep:
   ```sh
   grep -rE "claim_resolutions|policy_override|on_commit:|on_give_up:|release_to_back|release_to_head" test/scenarios/locks/ test/scenarios/attributes/
   ```
   If matches surface, address per the same playbook.

**Verification:**
```sh
go test ./test/scenarios/... -count=1
```
Expect: pass. Scenario tests use testcontainers; Docker required.

```sh
go test ./test/scenarios/locks/... ./test/scenarios/stores/... -race -count=2
```
Expect: pass.

---

## Task 17 — Smoke test + fixture

**Files:**
- `test/smoke/fixtures/template.yml`
- `test/smoke/stores_redesign_smoke_test.go`
- `test/smoke/setup.go` (audit)

**Steps:**

1. In `test/smoke/fixtures/template.yml`:
   - Delete the `claim_resolutions:` block (lines ~95-98). The block currently has a header line, a `- source: claim-topic / store: topics-ring` entry, and a comment line.

2. In `test/smoke/stores_redesign_smoke_test.go`:
   - Delete the `"claim_resolutions": map[string]any{...}` literal in the deployed body (lines ~592-597).
   - Rewrite the package-level comment at line ~683 (the "the acquirer's `claim_resolutions` block governs..." paragraph) to describe the per-substrate-config defaults model.
   - Audit the test's assertions for any expectation of `policy_override` content; drop if present.

3. In `test/smoke/setup.go`:
   - Audit for any `claim_resolutions` or `policy_override` references; scrub.

**Verification:**
```sh
go build ./test/smoke/...
```
Expect: clean build.

```sh
go test ./test/smoke/... -count=1
```
Expect: pass. Smoke uses testcontainers; Docker required. The smoke test runs 100 sequential force-fires through the control-api admin route; expect the run to take a few minutes.

---

## Task 18 — Documentation cascade

**Files:**
- `CLAUDE.md`
- `docs/glossary.md`
- `docs/architecture.md`
- `docs/operator-guide.md`
- `CHANGELOG.md`

**Steps:**

1. In `CLAUDE.md`:
   - Find the standing reference to "5+1-verb gRPC protocol" and replace with "4+1-verb."
   - Find the gotcha note about `RIMSKY_STORES_CONFIG` (search "RIMSKY_STORES_CONFIG"). Drop the parenthetical examples `(release_to_back, release_to_head, delete)` from the auto-terminal description and rewrite the routing as "success → Commit; failure → Abandon."
   - Find any other mentions of `claim_resolutions`, `policy_override`, or `Store.Delete` and update.
   - Update the "stores-redesign-v3-design.md is the current contract" line to also cite the new spec at `docs/specs/2026-04-30-stores-protocol-cleanup-design.md` as the authoritative cleanup overlay.

2. In `docs/glossary.md`:
   - Find the existing "pick policy" entry. Delete it from the rimsky-vocabulary section.
   - At the end of the document (or after the rimsky-vocabulary entries), add a new section:
     ```markdown
     ## Substrate-internal vocabulary (not part of rimsky's protocol surface)

     The terms below are used by some substrate-service implementations
     (e.g. the postgres reference store-service) but do not appear in the
     rimsky↔store wire protocol or the rimsky-side template grammar.
     They appear only in store-service-specific documentation and config.

     **pick policy** — An items-table queue convention some substrates
     implement. The postgres reference store-service exposes per-policy
     `on_commit_default` / `on_give_up_default` config in its own
     `config.yml`. See `docs/store-author-guide.md` and
     `deploy/store-postgres.yml`.

     **release_to_back / release_to_head** — Per-policy disposition
     actions in pick-policy substrates' configs. Substrate-internal;
     not visible to rimsky.

     **items-table delete (action)** — A per-policy disposition action
     in pick-policy substrates that removes the row from the items
     table. Distinct from any rimsky-level concept.
     ```
   - All other rimsky-vocabulary entries (`claim`, `region`, `selector`, `store`, `named lock`, etc.) keep their existing prose unchanged.

3. In `docs/architecture.md`:
   - Find references to "5 + 1" or "5+1" verbs and update to "4 + 1" / "4+1."
   - Find §1.2 (or wherever the postgres reference store is described) and rewrite "supports regional access AND substrate-side pick policies" to "supports regional access AND items-table queue semantics implemented substrate-internally."
   - Update any prose that lists the `Delete` verb or `policy_override` field.

4. In `docs/operator-guide.md`:
   - Find the timing-constraint discussion (search "visibility_timeout"). Relabel it as guidance for operators of *the postgres reference store-service* specifically — clarify it's not a rimsky-level constraint.
   - Drop any references to `claim_resolutions` template grammar.

5. In `CHANGELOG.md`:
   - Append a new bullet under the existing `## Unreleased` section, above the existing v3 entries:
     ```markdown
     - **Stores Protocol Cleanup — substrate-vocabulary excision.**
       Drops `policy_override` from `CommitRequest` / `AbandonRequest`,
       deletes the `Delete` wire verb (4+1 verbs, was 5+1), replaces
       `OpenResponse`'s implicit all-empty-bytes pool-empty signal with
       an explicit `oneof Acquired | Unavailable` discriminator, and
       removes the `claim_resolutions` template grammar
       (`node.ClaimResolution` Go type deleted; `selectResolutionAction`
       and `fireResolutionVerb` deleted from `core/supervisor/auto_terminal.go`).
       Substrate disposition (commit-vs-release-vs-delete on the
       substrate's own state) is governed entirely by per-substrate
       config (e.g. the postgres reference store's per-pick-policy
       `on_commit_default` / `on_give_up_default`). Bridge handler
       switches from `encoding/json` to `protojson` for response
       marshaling so the new oneof round-trips correctly. Spec:
       `docs/specs/2026-04-30-stores-protocol-cleanup-design.md`.
       Supersedes v3 §4.1 / §4.5 / §4.7 third-paragraph / §4.10
       invariant 13.1 / §5.1 / §5.2 / §7.8 obligation #3.
     ```

**Verification:**
```sh
git diff --stat CLAUDE.md docs/glossary.md docs/architecture.md docs/operator-guide.md CHANGELOG.md
```
Expect: all five files modified.

```sh
grep -nE "claim_resolutions|policy_override|release_to_back|release_to_head|5\+1.verb|5 \+ 1" CLAUDE.md docs/architecture.md docs/operator-guide.md
```
Expect: no matches in CLAUDE.md / architecture.md / operator-guide.md. (Glossary is allowed to mention the substrate-internal vocabulary — under the labeled section.)

---

## Task 19 — Mark v3-completion Issues 1 + 3 resolved

**Files:**
- `docs/history/v3-completion.md`

**Steps:**

1. In `docs/history/v3-completion.md`, locate the headers for Issue 1 (`## Issue 1 — Open error vs. "pool-empty" signal: rimsky shouldn't be guessing`) and Issue 3 (`## Issue 3 — Pick-policy excision: rimsky-side surface still leaks substrate vocabulary`).

2. Append a "**Status:** Resolved by `docs/specs/2026-04-30-stores-protocol-cleanup-design.md` (cycle landed YYYY-MM-DD)." line directly after each Issue's `## Issue N — ...` header. Use the literal string `(cycle landed by the implementing user)` if a date is not yet known — leave the sentence template in place either way.

3. Issue 2 (frame-engine multi-source observation), the lower-priority follow-ups, and the design-deferred items per spec §15 stay as-is.

**Verification:**
```sh
grep -nE "Status: Resolved" docs/history/v3-completion.md
```
Expect: at least two matches (one per resolved issue).

---

## Task 20 — Final full-build verification

**Files:** none (verification only).

**Steps:**

1. Full build:
   ```sh
   go build ./...
   ```
   Expect: clean.

2. Full unit + integration test pass (testcontainers used by some packages; Docker required):
   ```sh
   go test ./... -count=1
   ```
   Expect: clean. Coverage spans `core/`, `stores/`, `executors/`, `conformance/`, and `test/` packages.

3. Race-sensitive path coverage:
   ```sh
   go test ./core/queue/... ./core/supervisor/... ./core/scheduler/... -race -count=3
   ```
   Expect: clean.

4. Lint:
   ```sh
   make lint
   ```
   Expect: clean.

5. Module hygiene:
   ```sh
   make tidy
   git diff go.mod go.sum
   ```
   Expect: no diff (the cleanup adds no new deps and removes none — `protojson` and `proto` are already in `google.golang.org/protobuf` which is already a direct dep of the generated bindings).

6. Cross-language touch surface (the TS executor — v3 didn't change the executor protocol; this cycle doesn't either; verify):
   ```sh
   cd executors/claude-agent && npm install && npm test && npm run build
   cd -
   ```
   Expect: clean. (No code in this cycle touches `executors/`; this is a regression check.)

7. Verify the `claim_resolutions` and `policy_override` symbols are gone from the working tree (excluding spec, plan, glossary, v3-completion historical references):
   ```sh
   grep -rnE "claim_resolutions|policy_override" \
     --include="*.go" --include="*.proto" --include="*.yml" --include="*.yaml" \
     --exclude-dir=proto/v1/gen \
     . | \
     grep -vE "docs/specs/|docs/glossary\.md|docs/v3-completion\.md|CHANGELOG\.md|docs/plans/|docs/2026-04-2[56]-" \
     | head
   ```
   Expect: no output. (The exclude list filters historical material that legitimately references the old terms.)

8. Verify the `Delete` wire verb is gone from the proto and substrate impls:
   ```sh
   grep -nE "rpc Delete\(|DeleteRequest|DeleteResponse" proto/v1/store_service.proto
   grep -rnE "func \(s \*Store\) Delete\(" stores/
   ```
   Expect: no output.

9. Verify the `OpenResponse` oneof shape:
   ```sh
   grep -A4 "message OpenResponse" proto/v1/store_service.proto
   ```
   Expect: shows the `oneof result { Acquired ...; Unavailable ...; }` shape.

---

## Manual checks after completion

- **Smoke against the running stack.** After the implementer reports clean, the user may bring up `deploy/docker-compose.yml` and run T56-style health (`curl http://localhost:8080/health`) and T57-style conformance (the http-node + claude-agent endpoints) to confirm the runtime stack still builds end-to-end. The plan does not run this because v3 already passed it (this cycle's surface is rimsky-side and substrate-side; the executor protocol is untouched).
- **Helm chart audit.** `deploy/kubernetes/rimsky-chart/` is documented as stale in CLAUDE.md. This cycle does not propagate proto/grammar changes to the Helm chart; if/when the chart is refreshed, the cleanup-cycle artifacts (no `Delete` route, oneof Open response, no `claim_resolutions` env-passed templates) should be picked up at that time.
