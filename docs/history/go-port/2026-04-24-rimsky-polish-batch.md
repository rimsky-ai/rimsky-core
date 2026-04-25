# Rimsky Go Port — Polish Batch Implementation Plan

**Goal:** Close out 6 polish items surfaced during the post-implementation review of the rimsky Go port: four targeted code changes and two operator-side validations.

**Architecture:** All edits confined to `rimsky-go/`. No spec revisions in this plan (spec amendments are tracked separately in the execution log). Each task is small-scope, already-designed, and independently verifiable. Parallelizable where noted; otherwise serial.

**Tech stack:** Go 1.25+, Postgres 14+, Docker, Docker Compose v2, Helm 3+, `testcontainers-go`, existing reference implementations under `rimsky-go/`.

**Reference documents:**
- Spec: `docs/specs/2026-04-23-rimsky-go-port-design.md`
- Execution log: `docs/plans/2026-04-23-rimsky-go-port-execution-log.md` (for deviation numbering)

---

## Scope

Six tasks, each scoped to 5–60 minutes of implementation work.

| # | Origin | Summary |
|---|---|---|
| T1 | Deviation #4 | `core/scheduler/schedule_ticker.go` — drop narrow consumer-side interfaces; use `storage.StorageBackend` directly (post-fix #3 cycle-free). |
| T2 | Deviation #7 | Add `node.ReasonDispatchImpossible`; state machine allows `stale → failed` under it; supervisor runner uses it for unresolved-executor. |
| T3 | Deviation #8 | Introduce `resource.NewRegistry() *Registry`; `SupervisorConfig` takes a `*Registry`; deprecate global `RegisterFactory`/`GetFactory` as shims. |
| T4 | Deviation #10 | `executors/http-node/server.go` runs userdata validation before the stub-mode branch. |
| T5 | Polish P3 | Install helm, run `helm lint` + `helm template --debug` against the chart, fix anything surfaced. |
| T6 | Polish P4 | Run `deploy/build-images.sh`, bring the compose stack up, run an end-to-end smoke (deploy template + instance + wait for commit), tear down, remove obsolete `version:` line from the compose file. |

**Dispatch ordering:**
- T1, T2, T3, T4 can run in parallel (different packages, no file overlap).
- T5 is independent of T1–T4 and can run in parallel.
- T6 depends on T4 being done (so the smoke test uses the fixed http-node image). T6 also depends on T5 being done IF we want helm to be part of the operator smoke — but T6 focuses on docker-compose, not helm, so the dependency is only nominal.

In practice: dispatch T1, T2, T3, T4, T5 in parallel; dispatch T6 after T4 completes.

---

## Task T1 — Cleanup `schedule_ticker.go` narrow interfaces

**Origin:** Deviation #4 — leftover workaround from before the `scheduler → node` cycle was broken by moving backoff.

**Why it's safe now:** `core/node/backoff.go` lives in `node/`, not `scheduler/`. `scheduler` → `storage` → `node` → nothing back to scheduler. The cycle is gone. The narrow-interface indirection in `schedule_ticker.go` has no remaining purpose.

**Files to touch:**
- `rimsky-go/core/scheduler/schedule_ticker.go` (edit — delete narrow interfaces, adapter structs; rewrite `ProcessSchedules` signature)
- `rimsky-go/core/scheduler/schedule_ticker_test.go` (edit — update test call sites; fake backend now implements `storage.StorageBackend`)
- `rimsky-go/core/scheduler/scheduler.go` (edit — remove adapter glue; call `ProcessSchedules` with `cfg.Storage` directly)

**Steps:**

1. Read `rimsky-go/core/scheduler/schedule_ticker.go`. Identify the interfaces to delete: `ScheduleBackendView`, `ScheduleStoreView`, `EventStoreView`, `ScheduleRowView`, `EventAppendInputView`.

2. Change `ProcessSchedules`'s signature:
   ```go
   // Before:
   func ProcessSchedules(ctx context.Context, sb ScheduleBackendView, disp MessageDispatcher, clock shared.Clock, log shared.Logger) (int, error)
   // After:
   func ProcessSchedules(ctx context.Context, sb storage.StorageBackend, disp MessageDispatcher, clock shared.Clock, log shared.Logger) (int, error)
   ```
   Add `import "github.com/fallguy/rimsky/core/storage"`. Replace all `ScheduleBackendView` method calls with the equivalent `storage.StorageBackend` calls: `sb.Schedules().DueBefore(...)`, `sb.Schedules().RecordFired(...)`, `sb.Events().Append(...)`. Types are now `storage.ScheduleRow` and `storage.EventAppendInput` (drop the `View` suffix usage everywhere).

3. In `schedule_ticker.go`, delete the five interface/type declarations: `ScheduleBackendView`, `ScheduleStoreView`, `EventStoreView`, `ScheduleRowView`, `EventAppendInputView`. The adapter helpers (`scheduleBackendAdapter`, `scheduleStoreAdapter`, `eventStoreAdapter`) live in `scheduler.go`, not `schedule_ticker.go` — delete them there in step 5.

4. Read `rimsky-go/core/scheduler/scheduler.go`. Find the call to `ProcessSchedules`:
   ```go
   _, _ = ProcessSchedules(ctx, scheduleBackendAdapter(cfg.Storage), scheduleDispatcherAdapter{...}, cfg.Clock, cfg.Logger)
   ```
   Replace with:
   ```go
   _, _ = ProcessSchedules(ctx, cfg.Storage, scheduleDispatcherAdapter{...}, cfg.Clock, cfg.Logger)
   ```
   The `scheduleDispatcherAdapter` for `MessageDispatcher` stays — that's a separate concern (bridging `InvalidateNode` into the `MessageDispatcher` interface; keep that). Only the backend adapter is deleted.

5. If `scheduleBackendAdapter`, `scheduleStoreAdapter`, `eventStoreAdapter`, or similar helpers are declared in `scheduler.go` or a sibling file, delete them.

6. Read `rimsky-go/core/scheduler/schedule_ticker_test.go`. Existing tests use ~100 lines of in-memory fakes (`fakeScheduleStore`, `fakeEventStore`, `fakeBackend`) implementing the narrow interfaces. Rewrite as Postgres-backed tests:
   - Replace all in-memory fakes with `pgtest.StartPostgres(ctx, t)` + `pgstorage.New(pool)`.
   - Test setup: insert template + instance + node + schedule via the real storage sub-stores.
   - Test assertions: use `sb.Events().List(...)` to read events; use `sb.Schedules().ListAll(...)` to inspect schedule rows after a tick.
   This is a non-trivial test-file rewrite — budget up to 30 minutes for it. The test coverage stays the same (6 tests: NextFireAt valid/invalid, ProcessSchedules nothing-due/fires-one/dispatcher-error/invalid-cron-skips); only the setup/assertion plumbing changes.

6a. Read `rimsky-go/core/scheduler/scheduler_test.go`. Line ~307 has a compile-time assert `var _ ScheduleBackendView = scheduleBackendAdapter{}` (or similar). DELETE this line — both types no longer exist after this task.

7. Verify:
   ```
   cd /Users/claude/Documents/projects/zonebase/rimsky-go
   go build ./core/scheduler/...
   go test ./core/scheduler/... -run 'TestNextFireAt|TestProcessSchedules' -count=1 -timeout 120s
   go test ./core/scheduler/... -count=1 -timeout 180s  # full scheduler suite
   ```
   All must pass. Existing scheduler tests (`TestScheduler`, `TestInvalidateNode`, etc.) should continue to pass unchanged.

8. Verify import graph has no new cycles: `go vet ./...` passes cleanly.

**Done when:** scheduler package builds, full scheduler test suite passes, the five narrow interface types no longer exist in any scheduler file.

---

## Task T2 — `ReasonDispatchImpossible` + unresolved-executor direct stale→failed

**Origin:** Deviation #7 — the current shim transitions stale→running→failed for unresolved executors, which is dishonest in the event log.

**Files to touch:**
- `rimsky-go/core/node/state.go` (edit — add new reason + new case in `NextState`)
- `rimsky-go/core/node/state_test.go` (edit — add one new test)
- `rimsky-go/core/supervisor/runner.go` (edit — replace the unresolved-executor shim)
- `rimsky-go/test/scenarios/unresolved_executor_test.go` (edit — update event assertions if needed)

**Steps:**

1. Read `rimsky-go/core/node/state.go`. After the `ReasonPureCascade` declaration, add:
   ```go
   // ReasonDispatchImpossible transitions `stale → failed` directly when the
   // supervisor determines a node cannot be dispatched at all (e.g. the
   // template references an executor name not configured on any supervisor).
   // Unlike `ReasonPolicyGiveUp`, there is no policy chain involved — the
   // failure is infrastructural, not application-level, and the node never
   // entered `running`. Event log reflects the stale→failed transition
   // honestly.
   ReasonDispatchImpossible = TransitionReason{Kind: "dispatch_impossible"}
   ```

2. In `NextState`, inside the `case shared.NodeStateStale:` block, add:
   ```go
   if reason.Kind == "dispatch_impossible" { return shared.NodeStateFailed, nil }
   ```

3. Read `rimsky-go/core/node/state_test.go`. Add a new test:
   ```go
   func TestDispatchImpossibleTransitionsStaleToFailed(t *testing.T) {
       t.Parallel()
       got, err := NextState(shared.NodeStateStale, ReasonDispatchImpossible)
       require.NoError(t, err)
       require.Equal(t, shared.NodeStateFailed, got)
   }

   func TestDispatchImpossibleRejectedFromNonStale(t *testing.T) {
       t.Parallel()
       for _, from := range []shared.NodeState{shared.NodeStateFresh, shared.NodeStateRunning, shared.NodeStateFailed} {
           _, err := NextState(from, ReasonDispatchImpossible)
           require.ErrorIs(t, err, shared.ErrIllegalTransition, "from=%s", from)
       }
   }
   ```

4. Update `TestTransitionTable` in `state_test.go:34`. The test iterates every reason × every state; add `ReasonDispatchImpossible` to its cases with `stale → failed` as the only valid target, and assert `ErrIllegalTransition` from `fresh`, `running`, and `failed`.

5. Verify the node package builds + tests pass:
   ```
   cd /Users/claude/Documents/projects/zonebase/rimsky-go
   go test ./core/node/... -count=1
   ```

6. Read `rimsky-go/core/supervisor/runner.go`. Find the block that handles unresolved executors. It currently does:
   ```go
   // (approximate — verify exact code in situ)
   _ = sb.Nodes().UpdateState(ctx, args.NodeID, shared.NodeStateRunning, node.ReasonDispatchClaimed, nil)
   if err := OnError(ctx, OnErrorArgs{... ErrorClass: "unresolved_executor", ...}); err != nil { ... }
   ```

7. Replace with a direct-to-failed path:
   ```go
   // Direct stale → failed via ReasonDispatchImpossible. No on_error, no policy
   // chain — this is an infrastructural failure, not an application error.
   if err := sb.Nodes().UpdateState(ctx, args.NodeID, shared.NodeStateFailed, node.ReasonDispatchImpossible, nil); err != nil {
       return RunnerResult{Ran: true}, err
   }
   _ = sb.Events().Append(ctx, storage.EventAppendInput{
       NodeID: &args.NodeID, InstanceID: &nd.InstanceID,
       Kind: "error",
       Payload: map[string]any{
           "error_class":  "unresolved_executor",
           "details":      map[string]any{"executor_name": nd.Executor},
           "action_taken": "dispatch_impossible",
       },
   }, nil)
   return RunnerResult{Ran: true}, nil
   ```
   Keep the `unresolved_executor` event append that runs before the transition — that stays unchanged.

8. Verify supervisor package builds + tests pass:
   ```
   go test ./core/supervisor/... -count=1 -timeout 180s
   ```

9. Read `rimsky-go/test/scenarios/unresolved_executor_test.go`. Verify the assertions. The scenario test previously expected the event sequence to include a `running` state transition; now it should expect:
   - `unresolved_executor` event
   - `error` event with `action_taken: "dispatch_impossible"`
   - `state_transition` from stale → failed with `reason: "dispatch_impossible"`
   
   Update the test's assertions to match the new honest event trail.

10. Run the scenario test:
    ```
    go test ./test/scenarios/ -run TestUnresolvedExecutor -count=1 -timeout 120s
    ```
    Must pass.

11. Run the full suite to confirm no regression:
    ```
    go test ./... -count=1 -timeout 600s
    ```

**Done when:** `TestDispatchImpossibleTransitionsStaleToFailed` passes, `TestUnresolvedExecutor` scenario passes with the new event trail, full suite green.

---

## Task T3 — Explicit `*resource.Registry` type

**Origin:** Deviation #8 — global mutable factory registry is a footgun under parallelism and blocks multi-orchestrator-per-process use cases.

**Design decision (settled, no brainstorm needed):** introduce a `*resource.Registry` value type passed explicitly. Keep the global `RegisterFactory`/`GetFactory` free functions as deprecated shims over a package-default registry, for backward-compat with code already using them.

**Files to touch:**
- `rimsky-go/core/resource/register.go` (edit — add `Registry` type; make free functions delegate to a package-default)
- `rimsky-go/core/config/supervisor.go` (edit — `SupervisorConfig` gains `ResourceFactories *resource.Registry`)
- `rimsky-go/core/config/controlapi.go` (edit — `ControlAPIConfig` gains `ResourceFactories *resource.Registry`)
- `rimsky-go/core/supervisor/supervisor.go` (edit — `Config` gains `ResourceFactories`; plumb through to resource factory lookups)
- `rimsky-go/core/supervisor/runner.go` (edit — `GetResource` callback uses the registry, not global `GetFactory`)
- `rimsky-go/core/controlapi/app.go` (edit — `AppDeps` gains `ResourceFactories *resource.Registry`; app constructor threads it through)
- `rimsky-go/core/controlapi/templates.go` (edit — `ValidateTemplate` call site receives the registry via a closure over `deps.ResourceFactories`)
- `rimsky-go/core/controlapi/instances.go` (edit — replace `resource.GetFactory(impl)` with `deps.ResourceFactories.Get(impl)`)
- `rimsky-go/core/controlapi/app_test.go` (edit — tests construct and pass a per-test `*resource.Registry`)
- `rimsky-go/core/scenario/harness.go` (edit — construct per-test Registry; drop the current workaround; pass via both SupervisorConfig AND ControlAPIConfig)
- `rimsky-go/core/cmd/rimsky-supervisor/main.go` (edit — create a Registry, register factories into it, pass to SupervisorConfig)
- `rimsky-go/core/cmd/rimsky-control-api/main.go` (edit — create a Registry, register factories into it, pass to ControlAPIConfig)

**Naming note:** the config field is `ResourceFactories` (not `ResourceRegistry`) to avoid collision with the unrelated existing `storage.ResourceRegistry` interface at `core/storage/interfaces.go:169` — two different things, both legitimate, both in-scope for the supervisor/control-api packages. Keep the names distinct.

**Steps:**

1. Read `rimsky-go/core/resource/register.go`. Add:
   ```go
   // Registry is an explicit, non-global factory registry. Prefer this over
   // the package-level RegisterFactory/GetFactory functions when multiple
   // orchestrators run in the same process (e.g. parallel tests).
   type Registry struct {
       mu        sync.RWMutex
       factories map[string]Factory
   }

   func NewRegistry() *Registry {
       return &Registry{factories: map[string]Factory{}}
   }

   func (r *Registry) Register(name string, f Factory) {
       r.mu.Lock(); defer r.mu.Unlock()
       r.factories[name] = f
   }

   func (r *Registry) Get(name string) (Factory, bool) {
       r.mu.RLock(); defer r.mu.RUnlock()
       f, ok := r.factories[name]
       return f, ok
   }

   func (r *Registry) ListNames() []string {
       r.mu.RLock(); defer r.mu.RUnlock()
       names := make([]string, 0, len(r.factories))
       for n := range r.factories { names = append(names, n) }
       return names
   }
   ```

2. Rewrite the global functions as thin shims over a package-default registry:
   ```go
   var defaultRegistry = NewRegistry()

   // Deprecated: prefer Registry.Register on an explicit *Registry passed
   // through SupervisorConfig. The package-default registry is process-global
   // and not safe under parallel multi-orchestrator use.
   func RegisterFactory(name string, f Factory) { defaultRegistry.Register(name, f) }

   // Deprecated: prefer Registry.Get on an explicit *Registry.
   func GetFactory(name string) (Factory, bool) { return defaultRegistry.Get(name) }

   // DefaultRegistry returns the process-global registry. Provided for
   // consumers still using RegisterFactory/GetFactory; new code should
   // construct its own Registry via NewRegistry.
   func DefaultRegistry() *Registry { return defaultRegistry }
   ```

3. Delete the prior `factoryMu sync.RWMutex` + `factories map[string]Factory` + `ListFactoryNames` — replaced by the Registry methods above. Or keep `ListFactoryNames` as another shim: `func ListFactoryNames() []string { return defaultRegistry.ListNames() }`.

4. Verify the resource package builds + the inline-jsonb tests pass:
   ```
   cd /Users/claude/Documents/projects/zonebase/rimsky-go
   go build ./core/resource/...
   go test ./core/resource/... -count=1
   ```

5. Read `rimsky-go/core/config/supervisor.go`. Add `ResourceFactories *resource.Registry` to `SupervisorConfig`. If nil at `StartSupervisor`, fall back to `resource.DefaultRegistry()` for backward-compat.

6. Read `rimsky-go/core/supervisor/supervisor.go`. Add `ResourceFactories *resource.Registry` to the inner `Config`. `StartSupervisor` threads `cfg.ResourceFactories` into the inner config.

6a. Read `rimsky-go/core/config/controlapi.go`. Add `ResourceFactories *resource.Registry` to `ControlAPIConfig`. Same nil-fallback to `resource.DefaultRegistry()`.

6b. Read `rimsky-go/core/controlapi/app.go`. Add `ResourceFactories *resource.Registry` to `AppDeps`. `NewApp` threads it through. Update `rimsky-go/core/controlapi/templates.go`: the `ValidateTemplate` call currently uses `func(name string) bool { _, ok := resource.GetFactory(name); return ok }`; change to `func(name string) bool { _, ok := deps.ResourceFactories.Get(name); return ok }`. Update `rimsky-go/core/controlapi/instances.go`: replace `resource.GetFactory(rdef.Implementation)` with `deps.ResourceFactories.Get(rdef.Implementation)`. Update `rimsky-go/core/controlapi/app_test.go` to construct its own `reg := resource.NewRegistry()`, register inline-jsonb into it, and pass via `AppDeps.ResourceFactories`.

7. Read `rimsky-go/core/supervisor/runner.go`. The `RunArgs` already has a `GetResource` callback that currently uses `resource.GetFactory(impl)`. That callback's implementation is the caller's — owned by whoever constructs `RunArgs` (the supervisor main loop + callback server). Update those construction sites (in `supervisor.go` and `callback.go`) so the callback uses the registry instead of the global:
   ```go
   GetResource: func(ctx context.Context, resourceID shared.UUID) (resource.Resource, error) {
       row, err := cfg.Storage.Resources().Get(ctx, resourceID, nil)
       if err != nil || row == nil { return nil, err }
       // Infer implementation from the resource row — v1 uses inline-jsonb
       // as the only implementation bound to the registry. If the resource
       // row someday carries the implementation name, use it here. Until
       // then, look up "inline-jsonb" by default.
       fac, ok := registry.Get("inline-jsonb")
       if !ok { return nil, fmt.Errorf("resource factory not registered") }
       return fac.Create(resource.Config{
           "_resource_id":   resourceID.String(),
           "_path":          row.ResourcePath,
           "_owner_node_id": row.OwnerNodeID.String(),
           "keep_versions":  row.KeepVersions,
       }, nil, nil)
   }
   ```
   Here `registry` is the `*resource.Registry` held by the supervisor loop. Same change in the callback server's construction path if applicable.

8. **Side-note for reviewers:** the long-term correct behavior is to record the implementation name per-resource (as a `impl TEXT` column on `rimsky_resources`) so the supervisor can look up the right factory regardless of how many implementations are registered. That's a schema change and out of scope for this polish task. Document as a known limitation: v1 assumes the registry has a factory registered under the name used in the template's `owns_resources[].implementation`; if a node owns multiple resources with different implementations, the current lookup is incorrect. Track for post-v1.

9. Read `rimsky-go/core/scenario/harness.go`. The harness currently does the clobber-avoidance workaround by constructing `inlinejsonb.Factory` directly. Change: construct a per-harness `*resource.Registry`, register inline-jsonb into it, pass to `config.StartSupervisor` via `SupervisorConfig.ResourceFactories`. Drop the workaround code.

10. Read `rimsky-go/core/cmd/rimsky-supervisor/main.go`. Replace the global `resource.RegisterFactory("inline-jsonb", ...)` with:
    ```go
    reg := resource.NewRegistry()
    reg.Register("inline-jsonb", inlinejsonb.Factory{StorageRegistry: sb.Resources()})
    // reg.Register("external-sql", externalsql.Factory{...}) // when external-sql comes into scope
    ```
    Pass `reg` via `config.SupervisorConfig.ResourceFactories`.

11. Verify every package that touched the registry still builds:
    ```
    go build ./...
    go test ./core/resource/... ./core/supervisor/... ./core/scenario/... ./test/scenarios/... -count=1 -timeout 300s
    ```

12. Full suite:
    ```
    go test ./... -count=1 -timeout 600s
    ```

13. Run the Task-13.2 parallelism check: the scenario tests that run in parallel should all pass on one machine. This was the original bug; it should now be fixed by per-harness registries.

**Done when:** all tests pass with the new explicit registry, global `RegisterFactory`/`GetFactory` still work as deprecated shims, harness no longer uses the workaround.

**Review note for T3:** this task has the most surface area. Reviewer should check (a) no caller is left still using the global in production code paths, (b) the scenario harness parallelism bug is actually fixed (run `go test ./test/scenarios/... -count=10 -race` to stress it), (c) the "v1 assumes inline-jsonb" limitation from step 8 is documented.

---

## Task T4 — http-node stub mode runs userdata validation

**Origin:** Deviation #10 — currently stub mode accepts any userdata, which violates the protocol contract and fails the `malformed_userdata` conformance scenario.

**Files to touch:**
- `rimsky-go/executors/http-node/server.go` (edit — move validation before stub branch)
- `rimsky-go/executors/http-node/server_test.go` (edit — add one new test)

**Steps:**

1. Read `rimsky-go/executors/http-node/server.go`. Find `executeCore` (the shared function called by both gRPC Execute and the HTTP bridge). Current structure:
   ```go
   func (s *Server) executeCore(ctx context.Context, req *genv1.ExecuteRequest, send func(*genv1.ExecuteEvent) error) error {
       if err := send(heartbeat()); err != nil { return err }
       if s.stubMode { return s.executeStub(req, send) }  // ← validation happens AFTER this
       ud := req.Userdata.AsMap()
       urlStr, _ := ud["url"].(string)
       if urlStr == "" { return sendErrored(send, "invalid_userdata", "userdata.url required") }
       // ... real HTTP call ...
   }
   ```

2. Rearrange to validate BEFORE the stub-mode branch:
   ```go
   func (s *Server) executeCore(ctx context.Context, req *genv1.ExecuteRequest, send func(*genv1.ExecuteEvent) error) error {
       if err := send(heartbeat()); err != nil { return err }

       // Validate userdata shape even in stub mode — the protocol contract
       // requires executors to reject malformed input consistently, not only
       // in live mode. Spec §14.4 + conformance `malformed_userdata` scenario.
       ud := req.Userdata.AsMap()
       urlStr, _ := ud["url"].(string)
       if urlStr == "" {
           return sendErrored(send, "invalid_userdata", "userdata.url required")
       }

       if s.stubMode { return s.executeStub(req, send) }

       // ... real HTTP call below ...
   }
   ```

3. Add a new test in `server_test.go`:
   ```go
   func TestStubMode_RejectsMalformedUserdata(t *testing.T) {
       t.Parallel()
       cfg := Config{StubMode: true}
       srv := NewServer(cfg)
       // Call executeCore with userdata missing `url`. Verify it emits
       // Errored{error_class: "invalid_userdata"} instead of Complete.
       events := []*genv1.ExecuteEvent{}
       send := func(ev *genv1.ExecuteEvent) error { events = append(events, ev); return nil }
       ud, _ := structpb.NewStruct(map[string]any{}) // no url
       req := &genv1.ExecuteRequest{Userdata: ud}
       err := srv.executeCore(context.Background(), req, send)
       require.NoError(t, err)
       require.GreaterOrEqual(t, len(events), 2) // heartbeat + errored
       last := events[len(events)-1]
       errored, ok := last.Event.(*genv1.ExecuteEvent_Errored)
       require.True(t, ok, "last event should be Errored")
       require.Equal(t, "invalid_userdata", errored.Errored.ErrorClass)
   }
   ```

4. Verify:
   ```
   cd /Users/claude/Documents/projects/zonebase/rimsky-go
   go build ./executors/http-node/
   go test ./executors/http-node/... -count=1 -v
   ```
   The new test must pass. The existing `TestStubMode_ReturnsCannedResponse` must continue to pass (its userdata presumably has a `url` field or needs updating to include `stub_response: {...}`; if it uses no userdata at all, update it to include a valid `url`).

5. Run the conformance suite against a fresh http-node in stub mode to verify the `malformed_userdata` scenario now passes:
   ```
   (cd rimsky-go && go run ./executors/http-node &)  # RIMSKY_EXECUTOR_STUB_MODE=1 in env
   sleep 2
   go run ./core/cmd/rimsky-conformance --endpoint localhost:9091 --transport grpc --require-stub-mode
   # Expect 7 PASS, 1 SKIP, 0 FAIL (the prior malformed_userdata FAIL is now PASS)
   kill %1
   ```
   Adjust env setup as needed; if running interactively, use `RIMSKY_EXECUTOR_STUB_MODE=1 RIMSKY_EXECUTOR_HTTP_NODE_PORT=9091 go run ./executors/http-node`.

**Done when:** `TestStubMode_RejectsMalformedUserdata` passes; conformance suite against stub http-node reports 7 PASS / 1 SKIP / 0 FAIL.

---

## Task T5 — Helm chart lint

**Origin:** Polish P3 — chart wasn't validated during Plan C because helm wasn't installed.

**Files to touch:**
- Possibly `rimsky-go/deploy/kubernetes/rimsky-chart/*.yaml` (only if lint surfaces issues; otherwise no edits)

**Steps:**

1. Install helm:
   ```
   brew install helm
   helm version  # confirm ≥ v3.13
   ```
   If brew install fails due to permissions (similar to the earlier Go-install detour), download the binary directly:
   ```
   curl -fsSL https://get.helm.sh/helm-v3.16.0-darwin-amd64.tar.gz -o /tmp/helm.tar.gz
   tar -xzf /tmp/helm.tar.gz -C /tmp
   ln -sf /tmp/darwin-amd64/helm ~/.local/bin/helm
   helm version
   ```

2. Run lint:
   ```
   cd /Users/claude/Documents/projects/zonebase/rimsky-go
   helm lint deploy/kubernetes/rimsky-chart
   ```
   Expected: `0 chart(s) failed`. Any errors require fixing (likely: missing required value, malformed template, bad chart metadata).

3. Run template-render with a fake DSN to catch semantic issues:
   ```
   helm template rimsky deploy/kubernetes/rimsky-chart \
     --set postgres.dsn=postgres://user:pass@host:5432/rimsky \
     --debug > /tmp/rimsky-chart-render.yaml
   echo "Exit: $?"
   wc -l /tmp/rimsky-chart-render.yaml
   ```
   Exit 0 and a non-zero line count mean successful render.

4. (Optional but recommended) Pipe the render through `kubectl apply --dry-run=client -f -` to catch invalid Kubernetes YAML:
   ```
   if command -v kubectl >/dev/null; then
     cat /tmp/rimsky-chart-render.yaml | kubectl apply --dry-run=client -f - 2>&1 | tail -10
   else
     echo "kubectl not available; skipping Kubernetes-validity check"
   fi
   ```

5. If errors surface in steps 2 or 3, fix them in the chart templates under `deploy/kubernetes/rimsky-chart/templates/`. Most likely classes of issues:
   - Typo in a `{{ .Values.foo }}` reference.
   - Missing required field in `Chart.yaml` or `values.yaml`.
   - Un-closed template action (`{{ if ... }}` without `{{ end }}`).

6. Clean up the render output:
   ```
   rm -f /tmp/rimsky-chart-render.yaml
   ```

**Done when:** `helm lint` returns "0 chart(s) failed"; `helm template --debug` renders without error.

---

## Task T6 — docker-compose live smoke + remove obsolete `version:` line

**Origin:** Polish P4 — `docker compose up` was not exercised live during Plan C.

**Files to touch:**
- `rimsky-go/deploy/docker-compose.yml` (edit — remove obsolete `version:` line)
- Possibly fix anything surfaced by the smoke test.

**Prerequisite:** T4 complete (so the http-node image has the validation fix baked in).

**Steps:**

1. Remove the obsolete `version:` line from `deploy/docker-compose.yml`:
   ```
   cd /Users/claude/Documents/projects/zonebase/rimsky-go
   sed -i '' '/^version: /d' deploy/docker-compose.yml
   grep -c "^version:" deploy/docker-compose.yml  # must be 0
   ```

2. Validate compose config:
   ```
   docker compose -f deploy/docker-compose.yml config > /dev/null
   echo "Exit: $?"
   ```
   Exit 0 means still valid after the `version:` strip.

3. Build all images:
   ```
   RIMSKY_VERSION=0.1 ./deploy/build-images.sh
   docker image ls | grep '^rimsky/' | head -10
   ```
   Expect 6 images: `scheduler`, `supervisor`, `control-api`, `migrate`, `executor-http-node`, `executor-claude-agent`.

4. Bring up the stack:
   ```
   docker compose -f deploy/docker-compose.yml up -d
   docker compose -f deploy/docker-compose.yml ps
   ```
   All services should report healthy within 60 seconds. Common hiccups:
   - `rimsky-migrate` fails because of a DSN typo — fix the env.
   - Supervisor fails because supervisor-config.yml path is wrong — fix the volume mount.
   - claude-agent fails because `ANTHROPIC_API_KEY` is empty — verify `RIMSKY_EXECUTOR_STUB_MODE=1` is set (per the compose file, it should default to stub).

5. Smoke test against the live control API. **Use stub mode** (executor services in the compose file default to `RIMSKY_EXECUTOR_STUB_MODE=1`) so the smoke test doesn't depend on public-internet reachability or external services:
   ```
   curl -sSf http://localhost:8080/health
   echo "Health: $?"

   # Deploy a trivial template. Userdata still needs `url` to pass the http-node
   # validation gate (even in stub mode, see T4), but the value is ignored by
   # stub mode — any well-formed URL works.
   cat > /tmp/smoke-template.json <<'EOF'
   {
     "name": "smoke",
     "version": "1",
     "nodes": [
       {
         "type": "greet",
         "executor": "http-node",
         "userdata": { "url": "http://stub.local/ignored" }
       }
     ]
   }
   EOF
   TMPL_ID=$(curl -sSf -XPOST -H 'content-type: application/json' \
     -d @/tmp/smoke-template.json http://localhost:8080/templates | jq -r '.template_id')
   echo "Template deployed: $TMPL_ID"

   # Create an instance.
   INST_ID=$(curl -sSf -XPOST -H 'content-type: application/json' \
     -d "{\"template_id\":\"$TMPL_ID\",\"consumer_key\":\"smoke-1\",\"params\":{}}" \
     http://localhost:8080/instances | jq -r '.instance_id')
   echo "Instance created: $INST_ID"

   # Wait up to 30s for the greet node to reach fresh.
   for i in $(seq 1 30); do
     STATE=$(curl -sSf "http://localhost:8080/instances/$INST_ID/nodes" | jq -r '.nodes[0].state')
     if [ "$STATE" = "fresh" ]; then echo "Node reached fresh"; break; fi
     sleep 1
   done
   [ "$STATE" = "fresh" ] || { echo "Node did not reach fresh; state=$STATE"; exit 1; }
   ```

6. Tear down:
   ```
   docker compose -f deploy/docker-compose.yml down -v
   docker rmi rimsky/scheduler:0.1 rimsky/supervisor:0.1 rimsky/control-api:0.1 rimsky/migrate:0.1 \
              rimsky/executor-http-node:0.1 rimsky/executor-claude-agent:0.1 2>/dev/null || true
   rm -f /tmp/smoke-template.json
   ```
   Image removal is optional; keep if you want a warm cache for the next run.

7. Fix anything surfaced in steps 3–5. Most likely: a missing env var, a healthcheck timing tweak, an incorrect image tag in the compose file.

**Done when:** `docker compose up -d` brings all services healthy within 60 seconds; the smoke test posts a template, creates an instance, and observes the node reaching `fresh`; teardown is clean.

---

## Final verification (after all 6 tasks)

After T1–T6 all report done, run the full gate check:

```
cd /Users/claude/Documents/projects/zonebase/rimsky-go
go build ./...
go vet ./...
golangci-lint run
go test ./... -count=1 -timeout 600s -p=4

# Conformance against both executors in stub mode (manual — spin up, run, kill).
# Expect: 7 PASS, 1 SKIP, 0 FAIL per executor.
```

All must be green before declaring the batch done. Append a Plan-polish-batch entry to `rimsky-go/CHANGELOG.md` and the execution log.

---

## Appendix — Subagent dispatch notes

**Parallel batch 1:** T1, T2, T3, T4, T5 — all independent file sets.
**Serial after:** T6 (needs T4's http-node fix before building images).

**Critical-path tasks:**
- T3 (Registry) has the largest surface area; expect one review round.
- T6 (live compose) has the highest variance — docker image builds + live stack bring-up can surface anything from typos to healthcheck timing issues. Budget accordingly.
