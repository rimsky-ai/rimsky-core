# Divergences: 2026-05-15-control-plane-mcp-and-auth-plan

Audit of the working tree vs. what the plan literally said. Each item records the plan text, what landed, and a best-read inference of why. Not a critique; a record.

---

## 1. K-section: dry-run wired as early-exit helper, not `validate` / `execute` factoring

**What the plan said:** Section K opening ("Pattern") and every K1–K11 task: "Each handler factors into `validate(req) -> (validation, errors)` and `execute(req, validation) -> response`." K1 explicitly: "Factor into `validateCreateInstance` and `executeCreateInstance`."

**What was implemented:** A single shared helper `code:control/controlapi/dryrun.go::WriteDryRunResponse` returns a synthetic envelope when `ModeFromContext(ctx) == auth.ModeDryRun`. Each write handler keeps its original monolithic body and inserts a single early-exit `if WriteDryRunResponse(w, req, "would_have_X", details) { return }` after the validation steps it already had (see e.g. `code:control/controlapi/instances.go::handleCreateInstance#166`, `code:control/controlapi/templates.go::handleDeployTemplate#219`, `code:control/controlapi/nodes.go::handleInvalidateNode#145`, etc.). No `validateFoo` / `executeFoo` pair exists for any handler.

**Inferred reason:** The synthetic-response contract is what the spec actually requires; the plan's named `validate` / `execute` split was an implementation hint for *how* to thread mode through. The early-exit pattern achieves the same observable behaviour with substantially less churn — every handler keeps its existing positive-path test exercising the (unfactored) flow, and the dry-run path adds a single new test per handler against the helper. The implementer judged the split unnecessary once the synthetic response was centralised.

---

## 2. M2 conformance probe: not extended

**What the plan said:** M2 ("Conformance probe MCP extension") — modify `cmd/rimsky-conformance-probe/main.go` and add a new `cmd/rimsky-conformance-probe/auth_probe.go` with a `--mode=auth-mcp` switch that calls `POST /mcp initialize`, `tools/list`, `tools/call`, etc.

**What was implemented:** Nothing. `cmd/rimsky-conformance-probe/main.go` is unchanged; no `auth_probe.go`; no `--mode=auth-mcp` switch. Coverage instead lives in the in-process scenario test `code:test/scenarios/auth/lifecycle_test.go::TestMCPSkin_FiltersByGrant` which exercises `tools/list` filtering and `tools/call` permission denial against a running control-api inside the test process.

**Inferred reason:** The conformance probe binary in the codebase targets executor-conformance — it sets up an executor handshake and exercises async-callback paths. With MCP folded into control-api as a protocol skin (no separate process, no separate dial-in), the probe shape doesn't fit; the in-process test substitutes. The implementer's report flagged this explicitly. Spot-check: `TestMCPSkin_FiltersByGrant` covers the catalog-filtering and tools/call permission-denial assertions named in M2, but does not exercise `resources/list → method not found` or the `initialize` protocol-version handshake assertion.

---

## 3. `--key` flag conflict resolved by renaming to `--instance-key`

**What the plan said:** No mention of a flag-naming conflict. The plan defined the common `--key` flag for the auth subcommands (J3+) and assumed the existing `--key` flag on `instance create` and `run` would coexist.

**What was implemented:** Pre-existing `--key` flags on `instance create` and `run` were renamed to `--instance-key`. See `code:control/cli/instances.go#54` (`fs.StringVar(&instanceKey, "instance-key", ...)`), `code:control/cli/run.go#74` (same), and the usage line at `code:control/cli/run.go#86` reflecting the rename. The common `--key` flag now means API key everywhere via `code:control/cli/flags.go::RegisterCommonFlags`.

**Inferred reason:** The plan didn't notice the namespace collision. The implementer chose the cleaner global-meaning (`--key` = Bearer token) over preserving backward compatibility, which is consistent with the pre-v1 break-freely stance in `file:.claude/rules/rules.md`. This is a user-visible CLI break for those two subcommands; surfaced in `file:CHANGELOG.md` indirectly via the auth bullet but not called out as a rename.

---

## 4. EventTable shape mismatch: extra marshal/unmarshal round-trip

**What the plan said:** E2 (`code:control/controlapi/audit.go`) wrote:
```go
row := persistence.EventRow{
    ID:        shared.NewUUID(),
    Kind:      kind,
    Payload:   data,
    CreatedAt: s.Clock.Now(),
}
_ = s.Events.Insert(ctx, row)
```
i.e. it assumed an `EventRow{Kind string, Payload []byte}` interface and a direct `Insert` method.

**What was implemented:** The live persistence interface is `proto:EventAppendInput{Kind string, Payload map[string]any}` (see `code:foundation/persistence/events.go#25`). `code:control/controlapi/audit.go::insertEvent` marshals the typed payload to JSON, then unmarshals it back into a `map[string]any`, then wraps the insert in a `Tables.Transaction(...)` closure that calls `Events().Append(ctx, EventAppendInput{Kind, Payload: payloadMap}, tx)`. Same pattern in `code:runtime/auth_sweep.go::emitKeyRevoked`.

**Inferred reason:** The data-platform-extensions work that landed in `commit:c9c42bf` had already reshaped `EventTable` to take a structured `map[string]any` payload (because instance/node IDs need to be parsed out for indexing) and to require a `Tx` argument on every method. Reshaping persistence to take an opaque `[]byte` payload would have regressed the new indexing surface, so the audit emitter pays a marshal/unmarshal cost to bridge the typed payload structs to the wire shape persistence wants.

---

## 5. MCP catalog dispatch via `httptest.NewRecorder` instead of a custom recorder

**What the plan said:** H3 sketched `rec := newRecorder()` with a `BodyJSON()` helper — implying a small custom recorder type in the mcp package.

**What was implemented:** `code:control/controlapi/mcp/catalog.go::Invoke#170` uses `httptest.NewRecorder()` (stdlib) and a small `rawOrString(bs)` helper to parse the response body. The lazy `routerRef` for the in-process re-entry (the *other* H4 detail the implementer's report mentioned) IS implemented as the plan suggested in `code:control/controlapi/mcp_route.go::routerRef`; the `httptest.NewRecorder` is just a tactical choice for response capture, not a substitute for the lazy pointer.

**Inferred reason:** Stdlib `httptest.NewRecorder` is purpose-built for this and lives outside the package layering rules (it's `net/http/httptest`, not test-only). The plan's "newRecorder()" placeholder name suggested a custom shim; the implementer used the stdlib instead. The implementer's self-report described this as "in-process re-entry instead of the lazy `routerRef` shape" — that framing is misleading; both are present, the `routerRef` for the router pointer and `httptest.NewRecorder` for response capture.

---

## 6. APIKeyTable interface: every method takes a `Tx`; `WithTx` removed

**What the plan said:** A3 declared the interface with no `Tx` parameter on any method (e.g. `Insert(ctx context.Context, k APIKey) error`, `GetByID(ctx, id)`, etc.) and a dedicated `WithTx(ctx, func(tx APIKeyTable) error) error` method to scope a transaction. F5 (rotate) was sketched to call `keys.WithTx(ctx, func(tx persistence.APIKeyTable) error { ... })`.

**What was implemented:** `code:foundation/persistence/api_keys.go::APIKeyTable` adds a nullable `Tx` to every method (`Insert(ctx, k, tx)`, `GetByID(ctx, id, tx)`, `ActiveCount(ctx, now, tx)`, etc.). No `WithTx`. Rotation in `code:control/controlapi/auth_handlers.go::handleRotateKey#294` uses the shared `code:foundation/persistence.Tables.Transaction(ctx, func(ctx, tx) error {...})` instead, passing the same `tx` into `SetRevokeAt` and `Insert`. `MarkRevoked` also gained an extra `(bool, error)` return shape (the plan had it return `error`); `ActiveCount` now requires the caller to pass `now time.Time` rather than reading the server clock.

**Inferred reason:** The rest of the post-data-platform persistence package already follows the "every method takes a nullable Tx + use `Tables.Transaction` as the wrapper" pattern (see `code:foundation/persistence/messages.go`, `code:foundation/persistence/instances.go`, etc.). Adding a one-off `WithTx` shape on `APIKeyTable` alone would have been inconsistent. The implementer aligned with the prevailing house style. Likewise `ActiveCount(now)` mirrors the other tables that take `now` so the auth-middleware predicate uses the per-request injected clock rather than `time.Now()` baked into persistence.

---

## 7. `AuthState` holds `Tables`, not `Keys` + `Events` accessors

**What the plan said:** D2 declared `AuthState{ Keys persistence.APIKeyTable; Events persistence.EventTable; ... }` and D4 wired `Keys: persist.APIKeys(), Events: persist.Events()`.

**What was implemented:** `code:control/controlapi/auth_middleware.go::AuthState#40` holds a single `Tables persistence.Tables` handle. Call sites use `s.Tables.APIKeys()` and `s.Tables.Events()` (and `s.Tables.Transaction(...)` for audit writes). `code:control/config/controlapi.go#229` wires `Tables: persistStore`.

**Inferred reason:** Audit writes are wrapped in `Tables.Transaction(...)` (see #4), so AuthState already needed the umbrella handle for the transaction surface. Carrying two separate accessor pointers in addition would have been redundant — and the audit emitter would have needed a third `Tables` field anyway to call `.Transaction(...)`. Simpler: hold the umbrella.

---

## 8. `gate` shim allows `AuthState=nil` for tests

**What the plan said:** D6/D7 wrap every handler with `deps.AuthState.gateByAction(...)`. The plan assumed the AuthState is always non-nil in production and didn't sketch a test-side bypass.

**What was implemented:** `code:control/controlapi/auth_middleware.go::gate#304` wraps `gateByAction` with a nil-check: if `deps.AuthState == nil`, the inner handler runs unwrapped. Every route registration that previously took the bare handler now calls `gate(deps, "<action>", handler)` — see e.g. `code:control/controlapi/instances.go` etc.

**Inferred reason:** Existing per-handler tests construct an `AppDeps{}` literal without an `AuthState`; making the gate strict would have required every existing test to thread a real auth state through pgtest. The nil-permissive shim preserves the test surface. The production wiring at `code:control/config/controlapi.go` always sets a non-nil AuthState, so the bypass is unreachable in deployed code.

---

## 9. CLI auth subcommands live in `cmd/rimsky/`, not `control/cli/`

**What the plan said:** J2–J9 placed the auth subcommands at `cmd/rimsky/auth_*.go` (J2 added `case "auth": return authCmd(subargs)` in `cmd/rimsky/main.go`).

**What was implemented:** The plan was followed for placement, but the structure diverges from how every other CLI verb is organised post-rename. The other verbs (`template`, `instance`, `tag`, etc.) live in `control/cli/` and are dispatched from `cmd/rimsky/main.go` via `cli.RunTemplateRegister(...)`. The auth subcommands instead live in package `main` under `cmd/rimsky/auth_*.go` and are dispatched via the local `authCmd` function. The `auth_common.go` helper (`code:cmd/rimsky/auth_common.go`) is a separate auth-flavoured HTTP-client shim rather than reusing `control/cli/client.go` (which has its own `NewClient`).

**Inferred reason:** The plan literally said `cmd/rimsky/auth.go` etc., and the implementer obeyed. The fact that the rest of the post-rename CLI lives in `control/cli/` was not surfaced to the implementer as a hint to relocate. As a result, the auth subcommands have a duplicate HTTP-client helper (`authHTTPRequest`, `doAuthRequest`) parallel to `control/cli/client.go`. No `@source` annotation on the duplication.

---

## 10. CLI auth subcommands have no per-subcommand unit tests

**What the plan said:** J3–J9 each ended with a Verify command like `go test ./cmd/rimsky/... -count=1 -run TestAuthInit` (and -run TestAuthCreate, TestAuthList, TestAuthShow, TestAuthRevoke, TestAuthRotate, TestAuthStatus). J3 explicitly described an `httptest.Server` stub test for `authInit`.

**What was implemented:** Only `code:cmd/rimsky/auth_common_test.go` exists, covering `loadRole` and `applyGrantPatches` table-driven. No `TestAuthInit`, `TestAuthCreate`, `TestAuthList`, `TestAuthShow`, `TestAuthRevoke`, `TestAuthRotate`, `TestAuthStatus` against an httptest server. End-to-end coverage of the auth subcommand surface is in `code:test/smoke/auth_smoke_test.go::TestAuthSmoke_BootstrapLifecycle`, which invokes the HTTP surface directly (not the CLI functions).

**Inferred reason:** Once the smoke test covered the full lifecycle end-to-end, the per-subcommand unit tests became redundant. Each subcommand is a thin glue layer over `authHTTPRequest` / `doAuthRequest`, so the meaningful behaviour is in the common helpers (which are unit-tested) and in the end-to-end smoke (which exercises the wired calls). The implementer judged the per-subcommand stub tests low value vs. the smoke they already had.

---

## 11. L1–L9 scenario tests consolidated into one file

**What the plan said:** Section L lists nine separate test files: `test/scenarios/auth/bootstrap_test.go`, `permission_grant_test.go`, `dry_run_test.go`, `rotation_test.go`, `revoke_guard_test.go`, `anonymous_test.go`, `mcp_skin_test.go`, `audit_content_test.go`, `banner_test.go`.

**What was implemented:** Two files: `code:test/scenarios/auth/lifecycle_test.go` (contains `TestBootstrap_*`, `TestPermissionGrants_*`, `TestRotation_*`, `TestRevokeGuard_*`, `TestMCPSkin_*`, `TestAuditContent_*`, `TestAnonymousModeBanner_*`) and `code:test/scenarios/auth/dry_run_test.go`. The "anonymous transition" (L6) and "first-match-wins ordering" (L2) tests are subsumed into `lifecycle_test.go`'s permission/rotation tests rather than separate scenarios.

**Inferred reason:** One scenario file is easier to maintain than nine, and the test names within `lifecycle_test.go` (e.g. `TestBootstrap_AnonymousToAuthenticated`) preserve the L-section semantic categories. The implementer collapsed the file layout. Spot-checks of the scenarios named in the plan all exist as `Test*` functions, with one exception: the predicate-cache invalidation scenario explicitly called out in L6 ("cache TTL: after the first `IsAnonymousMode` call, mint a key; the next call within 1s may still return `anon: true` (cache); after `InvalidateAnonCache()` ... fresh value immediately") is not present as a dedicated test — the cache invalidation is covered only indirectly by the mint-then-authenticated transition.

---

## 12. Rotation-grace sweep wired in `control/config/scheduler.go`, not `cmd/rimsky-scheduler/main.go`

**What the plan said:** G2: "Locate `cmd/rimsky-scheduler/main.go`. Find the periodic-sweep dispatch loop ... Add a parallel goroutine or merge into the existing tick."

**What was implemented:** Sweep is started inside the existing scheduler-handle constructor at `code:control/config/scheduler.go::Start#137` via a `runAuthSweepLoop` goroutine (`code:control/config/scheduler.go#149`). `cmd/rimsky-scheduler/main.go` is unchanged.

**Inferred reason:** Per `commit:c9c42bf` and the post-data-platform refactors, the scheduler binary now thin-shims a `control/config` constructor that wires every scheduler-side sweep. Adding a third sweep at the binary level rather than the config level would have introduced a wiring path inconsistent with `BlobOrphans`, `OrphanBlobSweepInterval`, etc. The implementer co-located the wiring with the other sweeps.

---

## 13. `MCP protocolVersion` is "2025-06-18", not "2024-11-05"

**What the plan said:** H2 sketch literally `"protocolVersion": "2024-11-05"`.

**What was implemented:** `code:control/controlapi/mcp/server.go::handleInitialize#66` returns `"protocolVersion": "2025-06-18"`.

**Inferred reason:** MCP spec versions cycle. The implementer picked the more recent revision present in the SDK ecosystem when writing the code. Operator-visible only in the `initialize` response; no behaviour difference for the V1 tools-only surface.

---

## 14. MCP descriptions are inline in `ActionEntry`, not copied from old `mcp-servers/control-api/tools.go`

**What the plan said:** H3: "`descriptionFor(name)` is a hardcoded map of MCP-tool-name → human description (copy descriptions from the existing `mcp-servers/control-api/tools.go`)." H4: "`builtinSchemas()` returns the `map[string]json.RawMessage` ... copy from `mcp-servers/control-api/tools.go`".

**What was implemented:** `code:control/controlapi/actions.go::ActionEntry` gained a new `Description` field; every V1 entry has a freshly-authored one-line description (see e.g. "Invalidate a node (resumes if parked; otherwise marks stale + re-fires).") The MCP catalog's description hook `code:control/controlapi/mcp_route.go::descriptionForTool` reads `entry.Description`, falling back to `entry.Action`. `code:control/controlapi/mcp_route.go::builtinSchemas` returns an empty map — every tool defaults to `{"type":"object"}` via the catalog. No descriptions or schemas were copied from the deleted `mcp-servers/control-api/tools.go`.

**Inferred reason:** The H3/H4 "copy from" instructions presupposed that the legacy file was still on disk during fold-in. H1's "inventory" step + H6's "delete the standalone module" race; the implementer deleted first and re-authored second. The new descriptions are concise and uniform; the absence of per-tool schemas is a real coverage gap relative to the plan's "synthesize for new ones" intent — tools/list now exposes only the generic `{"type":"object"}` schema for every tool, which a strict MCP client may reject.

---

## 15. `ActionEntry.Description` field added (not in plan's ActionEntry shape)

**What the plan said:** C1's `ActionEntry` struct: `{ Action string; IsWrite bool; Routes []Route; MCPTools []string }`. No description field.

**What was implemented:** `code:control/controlapi/actions.go::ActionEntry#28` has an additional `Description string` field, populated for every V1 entry and consumed by the MCP catalog.

**Inferred reason:** Same forcing function as #14 — the description had to live somewhere once the legacy `tools.go` was deleted, and threading a separate description map alongside the registry felt redundant. Co-locating with the registry kept the surface single-purpose.

---

## 16. 401 response body carries `denial_reason`; plan said only `{"error": "unauthorized"}`

**What the plan said:** D2's `IdentityResolver` sketch: `writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})`.

**What was implemented:** `code:control/controlapi/auth_middleware.go#127` returns `{"error": "unauthorized", "denial_reason": "<value>"}` — the typed denial reason is surfaced in the response body in addition to the audit row.

**Inferred reason:** Operator-debugging affordance: a curl-against-control-api caller seeing a 401 can distinguish `no_token` from `revoked_token` from `expired_token` without grepping the event log. Plan didn't surface this in the body but didn't forbid it either; the implementer added it.

---

## 17. `GET /auth/keys` response wrapped in `{"keys": [...]}`, plan said bare array

**What the plan said:** F3: "Convert each row to a public DTO with no plaintext field. Return JSON array."

**What was implemented:** `code:control/controlapi/auth_handlers.go::handleListKeys#168` returns `writeJSON(w, http.StatusOK, map[string]any{"keys": out})` — wraps the slice in a `{keys: [...]}` envelope.

**Inferred reason:** Consistent with the rest of the control-api's list endpoints that wrap collections in named keys (versus the data-platform-extensions style). No spec or plan section that pins this either way; the implementer chose the conventional envelope.

---

## 18. `revoked_at` check on rotate returns 409, plan didn't specify

**What the plan said:** F5's `handleRotateKey` sketch: looks up the key, mints + sets revoke_at + inserts new row inside a tx. No explicit check that the old key is not already revoked.

**What was implemented:** `code:control/controlapi/auth_handlers.go::handleRotateKey#275` adds:
```go
if oldRow.RevokedAt != nil {
    writeJSON(w, http.StatusConflict, map[string]any{"error": "cannot rotate a revoked key"})
    return
}
```

**Inferred reason:** Defensive gate — rotating a revoked key creates a new active key with the revoked-key's permissions, which is operator-surprising. Probably an emergent finding during integration testing. Not surfaced in plan or spec; the implementer judged it correct semantics and added it.

---

## 19. `feature-index.md` has a stale `mcp-servers/control-api/` row

**What the plan said:** H6: "Delete the directory ... Grep the codebase for references to `mcp-servers/control-api`." N4: "feature-index.md ... Add entries for the new auth surface, MCP-as-skin, dry-run, rotation."

**What was implemented:** Auth + MCP-as-skin rows added (`code:feature-index.md` rows for `auth`, `controlapi`, `controlapi/mcp`, `cli`, `rimsky`). The `### MCP servers (mcp-servers/)` section and the `control-api | mcp-servers/control-api/` row are still present in `code:feature-index.md#130` despite the directory being deleted in H6.

**Inferred reason:** N4 told the implementer to add entries; H6 told them to delete code references; nothing explicitly directed scrubbing feature-index. The stale row escaped both passes. Minor doc drift.

---

## 20. CHANGELOG entry doesn't call out the `--key` → `--instance-key` CLI break

**What the plan said:** N1's CHANGELOG bullet was sketched and explicitly mentioned the rename, dry-run, MCP fold-in, bootstrap. No CLI flag-break was anticipated because divergence #3 wasn't anticipated.

**What was implemented:** `code:CHANGELOG.md#5` matches the plan's bullet but says nothing about the `--key` → `--instance-key` rename on `instance create` / `run`. Operator scripts pinning `instance create --key=...` will break silently (the flag is unknown and parses as a positional).

**Inferred reason:** Consequence of divergence #3 — the rename was a forced fix the implementer made under time pressure, and the CHANGELOG entry was authored from the plan's pre-written sketch.

---

## 21. Anonymous-mode banner: warns only via slog, no separate WARN-stream

**What the plan said:** D5 implies a single Logger.Warn call inside `CheckAnonymousBanner`. Spec text says "logs at WARN at startup and then every 5 minutes thereafter while in anonymous mode".

**What was implemented:** `code:control/controlapi/auth_banner.go::CheckAnonymousBanner#39` does exactly one `s.Logger.Warn(...)` call per invocation. Conforms to plan.

(Listed here as a non-divergence so reviewers don't re-investigate.)

---

## 22. `/health` exempt from auth; `/ready` not added

**What the plan said:** D3 lists both `/health` and `/ready` as auth-exempt.

**What was implemented:** Only `/health` exists in the codebase (`code:control/controlapi/health.go#38`). No `/ready` route; nothing to exempt. App-startup at `code:control/controlapi/app.go#160` mounts only health pre-auth.

**Inferred reason:** Pre-existing — the codebase never had a `/ready`; the plan over-anticipated. Non-issue.

---

## 23. MCP `/mcp` endpoint not gated by an action

**What the plan said:** D7's registry-vs-router cross-check test was meant to catch any handler not wired to `gateByAction`. The `/mcp` route is registered via `registerMCPRoute(rrr, deps)` after `registerAuthRoutes` (`code:control/controlapi/app.go#213`).

**What was implemented:** `code:control/controlapi/mcp_route.go::registerMCPRoute#73` registers `r.Post("/mcp", server.ServeHTTP)` directly — no `gateByAction` wrap. The MCP server runs unauthenticated at the route level, but every `tools/call` re-enters the chi router and is gated there, and the `tools/list` filter is identity-aware via the catalog's `IdentityFromContext` lookup. The plan didn't explicitly say `/mcp` should be gated by a registry action; there's no `mcp:invoke` action in the spec.

**Inferred reason:** The plan implicitly assumed every route is gated, but `/mcp` is the protocol-skin entry, not a logical action — its `initialize` and `tools/list` methods are administrative and need to fall back to anonymous identity in anonymous mode. The implementer judged that gating `/mcp` at the route level would require a new umbrella action; instead, the gate runs on the *invoked* tool, which is where the meaningful permission live. The plan's D7 cross-check test that would have flagged this also wasn't implemented (see #24).

---

## 24. `TestRegistryCoversRouter` not implemented

**What the plan said:** D7 step 3: "Add a registry-vs-router cross-check test in `actions_test.go`" — `TestRegistryCoversRouter` walks the chi router and asserts every route has a registry entry (excepting `/health` and `/v1/observability/*`).

**What was implemented:** No such test in `code:control/controlapi/actions_test.go` — only `TestActionRegistry_*` and `TestV1Registry`. Consequence: the implementation has no automated guard against a future route being registered without a `gateByAction` wrap.

**Inferred reason:** Likely time pressure or the implementer judged the per-route `gate(deps, "<action>", ...)` wrap to be visible-enough at registration that a test wasn't urgent. Real gap relative to plan.

---

## 25. K12 (`TestAuthDryRunIgnored`) not implemented as named test

**What the plan said:** K12's Verify command: `go test ./control/controlapi/... -count=1 -run TestAuthDryRunIgnored`.

**What was implemented:** No `TestAuthDryRunIgnored`. The K12 semantic (auth-mutation handlers ignore `ModeFromContext`) is implemented by *not calling* `WriteDryRunResponse` in `handleCreateKey` / `handleRevokeKey` / `handleRotateKey` (see comment block in `code:control/controlapi/auth_handlers.go#65`). The behaviour is not test-asserted; a future change that added a dry-run branch to those handlers would not be caught by any existing test.

**Inferred reason:** K12 is a no-op task as written — there's nothing to add to the handlers, just nothing to remove. The implementer dropped the test as redundant.

---

## 26. Smoke `BootCluster` not extended; auth-smoke is a sibling test

**What the plan said:** M1: "In `setup.go`'s `BootCluster` ... after the cluster comes up: Verify `GET /auth/status` returns anonymous. Run `rimsky auth init`. Capture the plaintext; set `RIMSKY_API_KEY` for downstream test helpers." This would have meant every other smoke test runs with a minted admin key.

**What was implemented:** `code:test/smoke/setup.go` is unchanged by this plan; the auth bootstrap lives only in `code:test/smoke/auth_smoke_test.go::TestAuthSmoke_BootstrapLifecycle`. Other smoke tests continue to run against anonymous-mode control-api (because the auth-smoke test runs its own SmokeStack rather than mutating a shared one).

**Inferred reason:** Threading a Bearer token through every smoke-test HTTP call would have been a wide change (every helper that hits the control-api). The implementer scoped the auth-bootstrap to one smoke test rather than retrofitting every smoke. Spec doesn't require the wider threading; plan was prescriptive on the implementation detail.

---

## 27. Default banner interval value matches plan; banner message constant extracted

**What the plan said:** D5 stored the banner message inline.

**What was implemented:** `code:control/controlapi/auth_banner.go::AnonymousModeBannerMessage` is exported as a const so the L9 scenario test (`TestAnonymousModeBanner_LogsAndStops`) can match on it without string-duplication. Minor extraction, but worth noting because it's a coupling between the WARN log and the test contract.

**Inferred reason:** Test ergonomics; not a deviation from intent.

---

## Tally

26 meaningful divergences (excluding #21 and #22, which are listed for completeness but represent conformance with intent or pre-existing state rather than choices the implementer made).
